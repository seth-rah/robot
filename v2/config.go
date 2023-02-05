package main

import (
	"context"
	"crypto/cipher"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"gitlab.com/zephyrtronium/sq"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/sha3"
	"golang.org/x/time/rate"

	"github.com/zephyrtronium/robot/v2/brain/sqlbrain"
	"github.com/zephyrtronium/robot/v2/brain/userhash"
	"github.com/zephyrtronium/robot/v2/channel"
	"github.com/zephyrtronium/robot/v2/distro"
	"github.com/zephyrtronium/robot/v2/privacy"
)

// Robot is the overall configuration for the bot.
type Robot struct {
	// brain is the brain.
	brain *sqlbrain.Brain
	// privacy is the privacy.
	privacy *privacy.DBList
	// channels are the channels.
	channels map[string]*channel.Channel
	// secrets are the bot's keys.
	secrets *keys
	// owner is the username of the owner.
	owner string
	// ownerContact describes contact information for the owner.
	ownerContact string
	// tmi contains the bot's Twitch OAuth2 settings. It may be nil if there is
	// no Twitch configuration.
	tmi *client
}

// Load loads Robot from a TOML configuration.
func Load(ctx context.Context, r io.Reader) (*Robot, error) {
	var cfg Config
	var robo Robot
	md, err := toml.NewDecoder(r).Decode(&cfg)
	if err != nil {
		return nil, fmt.Errorf("couldn't decode config: %w", err)
	}

	robo.secrets, err = readkeys(os.ExpandEnv(cfg.SecretFile))
	if err != nil {
		return nil, fmt.Errorf("couldn't read keys: %w", err)
	}

	br, pr, err := loadDBs(ctx, cfg.DB)
	if err != nil {
		return nil, err
	}
	robo.brain, err = sqlbrain.Open(ctx, br)
	if err != nil {
		return nil, fmt.Errorf("couldn't open brain: %w", err)
	}
	robo.privacy, err = privacy.Open(ctx, pr)
	if err != nil {
		return nil, fmt.Errorf("couldn't open privacy list: %w", err)
	}

	if md.IsDefined("tmi") {
		robo.tmi, err = loadClient(cfg.TMI)
		if err != nil {
			return nil, fmt.Errorf("couldn't load TMI config: %w", err)
		}
	}

	for nm, ch := range cfg.Twitch {
		blk, err := regexp.Compile("(" + cfg.Global.Block + ")|(" + ch.Block + ")")
		if err != nil {
			return nil, fmt.Errorf("bad global or channel block expression for twitch.%s: %w", nm, err)
		}
		emotes := distro.New(distro.FromMap(mergemaps(cfg.Global.Emotes, ch.Emotes)))
		var ign, mod map[string]bool
		for u, p := range ch.Privileges {
			// TODO(zeph): Users may be listed in the TOML as usernames or as
			// user IDs, but the maps we give back should be only IDs.
			switch {
			case strings.EqualFold(p, "ignore"):
				if ign == nil {
					ign = make(map[string]bool)
				}
				ign[u] = true
			case strings.EqualFold(p, "moderator"):
				if mod == nil {
					mod = make(map[string]bool)
				}
				mod[u] = true
			}
		}
		for _, p := range ch.Channels {
			v := channel.Channel{
				Name:      p,
				Learn:     ch.Learn,
				Send:      ch.Send,
				Block:     blk,
				Responses: ch.Responses,
				Rate:      rate.NewLimiter(rate.Every(fseconds(ch.Rate.Every)), ch.Rate.Num),
				Ignore:    ign,
				Mod:       mod,
				Memery:    channel.NewMemeDetector(ch.Copypasta.Need, fseconds(ch.Copypasta.Within)),
				Emotes:    emotes,
			}
			robo.channels[p] = &v
		}
	}

	robo.owner = cfg.Owner.Name
	robo.ownerContact = cfg.Owner.Contact

	return &robo, nil
}

// Init initializes the databases in a Robot TOML configuration.
func Init(ctx context.Context, r io.Reader, order int) error {
	if order <= 0 {
		return errors.New("order must be positive")
	}
	var cfg Config
	b, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("couldn't read config: %w", err)
	}
	if err := toml.Unmarshal(b, &cfg); err != nil {
		return fmt.Errorf("couldn't unmarshal config: %w", err)
	}
	brain, priv, err := loadDBs(ctx, cfg.DB)
	if err != nil {
		return err
	}
	if err := sqlbrain.Create(ctx, brain, order); err != nil {
		return fmt.Errorf("couldn't initialize brain: %w", err)
	}
	if err := privacy.Init(ctx, priv); err != nil {
		return fmt.Errorf("couldn't initialize privacy list: %w", err)
	}
	return nil
}

func loadDBs(ctx context.Context, cfg DBCfg) (brain, priv *sq.DB, err error) {
	bp := os.ExpandEnv(cfg.Brain)
	pp := os.ExpandEnv(cfg.Privacy)

	brain, err = sq.Open("sqlite3", bp)
	if err != nil {
		return nil, nil, fmt.Errorf("couldn't open brain db: %w", err)
	}
	if err := brain.Ping(ctx); err != nil {
		return nil, nil, fmt.Errorf("couldn't connect to brain db: %w", err)
	}

	if pp == bp {
		priv = brain
		return brain, priv, nil
	}

	priv, err = sq.Open("sqlite3", pp)
	if err != nil {
		return nil, nil, fmt.Errorf("couldn't open privacy db: %w", err)
	}
	if err := priv.Ping(ctx); err != nil {
		return nil, nil, fmt.Errorf("couldn't connect to privacy db: %w", err)
	}

	return brain, priv, nil
}

func mergemaps(ms ...map[string]int) map[string]int {
	u := make(map[string]int)
	for _, m := range ms {
		for k, v := range m {
			u[k] += v
		}
	}
	return u
}

func fseconds(s float64) time.Duration {
	return time.Duration(s * float64(time.Second))
}

// client is the settings for OAuth2 and related elements.
type client struct {
	// me is the bot's username. The interpretation of this is domain-specific.
	me string
	// owner is the user ID of the owner. The interpretation of this is
	// domain-specific.
	owner string
	// refresh is the OAuth2 refresh token file.
	refresh string
	// client is the application client ID.
	client string
	// secret is the application client secret.
	secret string
}

// loadClient loads client configuration from unmarshaled TOML.
func loadClient(t ClientCfg) (*client, error) {
	secret, err := os.ReadFile(os.ExpandEnv(t.SecretFile))
	if err != nil {
		return nil, fmt.Errorf("couldn't read client secret: %w", err)
	}
	return &client{
		me:      t.User,
		owner:   t.Owner,
		refresh: os.ExpandEnv(t.TokenFile),
		client:  t.CID,
		secret:  string(secret),
	}, nil
}

type keys struct {
	// userhash is the hasher for userhashes.
	userhash userhash.Hasher
	// oauth is the encrypter for OAuth2 refresh tokens.
	// TODO(zeph): this should probably move to a separate package to
	// handle all the oauth stuff, especially nonce
	oauth cipher.AEAD
}

// readkeys creates keys for userhashes and encryption from the base key at a
// given file.
func readkeys(file string) (*keys, error) {
	k, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var keys keys
	{
		kr := hkdf.Expand(sha3.New224, k, []byte("userhash"))
		p := make([]byte, 64)
		if _, err := kr.Read(p); err != nil {
			return nil, err
		}
		keys.userhash = userhash.New(p)
	}
	{
		kr := hkdf.Expand(sha3.New224, k, []byte("oauth"))
		p := make([]byte, chacha20poly1305.KeySize)
		if _, err := kr.Read(p); err != nil {
			return nil, err
		}
		keys.oauth, err = chacha20poly1305.New(p)
		if err != nil {
			return nil, err
		}
	}
	return &keys, nil
}

// Config is the marshaled structure of Robot's configuration.
type Config struct {
	// SecretFile is the path to a file containing a secret key used to encrypt
	// durable secrets like OAuth2 refresh tokens as well as to create
	// userhashes.
	SecretFile string `toml:"secret"`
	// Owner is the table of metadata about the owner.
	Owner Owner `toml:"owner"`
	// DB is the table of database connection strings.
	DB DBCfg `toml:"db"`
	// Global is the table of global settings.
	Global Global `toml:"global"`
	// TMI is the configuration for connecting to Twitch chat.
	TMI ClientCfg `toml:"tmi"`
	// Twitch is the set of channel configurations for twitch. Each key
	// represents a group of one or more channels sharing a config.
	Twitch map[string]ChannelCfg `toml:"twitch"`
}

// ChannelCfg is the configuration for a channel.
type ChannelCfg struct {
	// Channels is the list of channels using this config.
	Channels []string `toml:"channels"`
	// Learn is the tag used for learning from these channels.
	Learn string `toml:"learn"`
	// Send is the tag used for generating messages for these channels.
	Send string `toml:"send"`
	// Block is a regular expression of messages to ignore.
	Block string `toml:"block"`
	// Responses is the probability of generating a random message when
	// a non-command message is received.
	Responses float64 `toml:"responses"`
	// Rate is the rate limit for interactions.
	Rate Rate `toml:"rate"`
	// Copypasta is the configuration for copypasta.
	Copypasta Copypasta `toml:"copypasta"`
	// Emotes is the emotes and their weights for the channel.
	Emotes map[string]int `toml:"emotes"`
	// Privileges is the user access controls for the channel.
	Privileges map[string]string `toml:"privileges"`
}

// Global is the configuration for globally applied options.
type Global struct {
	// Block is a regular expression of messages to ignore everywhere.
	Block string `toml:"block"`
	// Emotes is the emotes and their weights to use everywhere.
	Emotes map[string]int `toml:"emotes"`
}

// Owner is metadata about the bot owner.
type Owner struct {
	// Name is the username of the owner.
	Name string `toml:"name"`
	// Contact describes owner contact information.
	Contact string `toml:"contact"`
}

// ClientCfg is the configuration for connecting to an OAuth2 interface.
type ClientCfg struct {
	// User is robot's username. The interpretation of this is domain-specific.
	// On TMI, it is used to connect and to detect commands.
	User string `toml:"user"`
	// CID is the client ID.
	CID string `toml:"cid"`
	// SecretFile is the path to a file containing the client secret.
	SecretFile string `toml:"secret"`
	// TokenFile is the path to a file in which the bot will persist its OAuth2
	// refresh token. It is encrypted with a key derived from the Config.Secret
	// key.
	TokenFile string `toml:"token"`
	// Owner is the user ID of the owner. The interpretation of this is
	// domain-specific.
	Owner string `toml:"owner"`
}

// DBCfg is the configuration of databases.
type DBCfg struct {
	Brain   string `toml:"brain"`
	Privacy string `toml:"privacy"`
}

// Rate is a rate limit configuration.
type Rate struct {
	Every float64 `toml:"every"`
	Num   int     `toml:"num"`
}

// Copypasta is a copypasta configuration.
type Copypasta struct {
	Need   int     `toml:"need"`
	Within float64 `toml:"within"`
}