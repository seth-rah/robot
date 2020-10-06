package commands

import (
	"context"
	"regexp"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/zephyrtronium/robot/brain"
	"github.com/zephyrtronium/robot/irc"
)

// Do performs the first command appropriate for the message and returns the
// name of the performed command, or the empty string if none. priv is the
// privilege level for the user and cmd is the command invocation as parsed by
// Parse.
func Do(ctx context.Context, br *brain.Brain, send chan<- irc.Message, msg irc.Message, priv, cmd string) string {
	for _, c := range all {
		if m := c.ok(priv, cmd); m != nil {
			c.f(ctx, br, send, msg, m)
			return c.name
		}
	}
	return ""
}

// Parse parses a command invocation from a message. A command invocation is a
// message beginning or ending with me, optionally preceded by @ or followed by
// punctuation.
func Parse(me, msg string) (cmd string, ok bool) {
	cmd = msg
	if msg[0] == '@' {
		msg = msg[1:]
	}
	if len(msg) < len(me) {
		return
	}
	if strings.EqualFold(me, msg[:len(me)]) {
		if len(msg) == len(me) {
			return "", true
		}
		r, n := utf8.DecodeRuneInString(msg[len(me):])
		if unicode.IsSpace(r) || r == ':' || r == ',' {
			return strings.TrimSpace(msg[len(me)+n:]), true
		}
	}
	r, n := utf8.DecodeLastRuneInString(msg)
	if unicode.IsPunct(r) {
		msg = strings.TrimSpace(msg[:len(msg)-n])
	}
	if len(msg) < len(me) {
		return
	}
	if !strings.EqualFold(me, msg[len(msg)-len(me):]) {
		return
	}
	if len(msg) == len(me) {
		return "", true
	}
	msg = msg[:len(msg)-len(me)]
	r, n = utf8.DecodeLastRuneInString(msg)
	if r == '@' || unicode.IsSpace(r) {
		return strings.TrimSpace(msg[:len(msg)-n]), true
	}
	return
}

type command struct {
	// disable indicates that a command should never be used, even by owners,
	// if nonzero.
	disable int32
	// admin and regular indicate whether admins and unprivileged users,
	// respectively, may use this command.
	admin, regular bool
	// name is the name of this command. Names should be unique and should
	// not contain space characters so that they can be enabled and disabled.
	name string
	// re is the regular expression to detect whether a message is invoking
	// this command. Commands are tested in order, so an earlier command may
	// override a later one; if the commands have different privilege
	// requirements, then this allows an admin or owner to invoke a different
	// command matching the same expression. To avoid spurious matches, the
	// expression should start with ^ and end with $, i.e. it should match the
	// entire line.
	re *regexp.Regexp
	// f is the function to invoke.
	f func(ctx context.Context, br *brain.Brain, send chan<- irc.Message, msg irc.Message, matches []string)
	// help is a short usage for the command.
	help string
}

// ok returns nil if the command should not be executed with this invocation or
// the submatches of the regular expression if it should.
func (c *command) ok(priv, invoc string) []string {
	if atomic.LoadInt32(&c.disable) != 0 {
		return nil
	}
	switch priv {
	case "owner": // always yes
	case "admin", "bot":
		if !c.admin {
			return nil
		}
	case "":
		if !c.regular {
			return nil
		}
	case "ignore":
		return nil
	}
	return c.re.FindStringSubmatch(invoc)
}

var all []*command

func init() {
	all = []*command{
		{
			admin: false,
			name:  "enable",
			re:    regexp.MustCompile(`(?i)^(?P<op>enable|disable)\s+(?P<name>\S+)$`),
			f:     enable,
			help:  `["enable|disable" command-name] Enable or disable a command globally.`,
		},
		{
			admin: false,
			name:  "resync",
			re:    regexp.MustCompile(`(?i)^resync(?:\s+with\s+the)?(?:\s+database)?$`),
			f:     resync,
			help:  `["resync"] Update all channel configurations, user privileges, and emotes from the database.`,
		},
		{
			admin: false,
			name:  "exec",
			re:    regexp.MustCompile(`^EXEC\s+(?P<query>.*)$`),
			f:     exec,
			help:  `["EXEC" query] Execute an arbitrary SQL query. Handle with care.`,
		},
		{
			admin: false,
			name:  "raw",
			re:    regexp.MustCompile(`(?i)^raw\s+(?P<cmd>\d{3}|[A-Z0-9]+)\s*(?P<params>(?:\s*[^: ]\S*)*)?\s*(?::(?P<trailing>.*))?$`),
			f:     raw,
			help:  `["raw" CMD params :trailing] Send a raw IRC message.`,
		},
		{
			admin: false,
			name:  "join",
			re:    regexp.MustCompile(`(?i)^join\s+(?P<channel>#\w+)\s*(?:(?P<learn>\S+)\s+(?P<send>\S+))?$`),
			f:     join,
			help:  `["join" channel (learn-tag send-tag)] Join a channel.`,
		},
		{
			admin: false,
			name:  "privs",
			re:    regexp.MustCompile(`(?i)^give\s+@?(?P<user>\S+)\s+(?P<priv>owner|admin|bot|regular|ignore)\s*(?:priv(?:ilege)?s?\s*)?(?:in\s+)?(?P<where>everywhere|#\w+)?`),
			f:     privs,
			help:  `["give" user owner|admin|bot|regular|ignore ("in" everywhere|#somewhere)] Modify a user's privileges.`,
		},
		{
			admin: false,
			name:  "quit",
			re:    regexp.MustCompile(`^quit$`),
			f:     quit,
			help:  `["quit"] Quit.`,
		},
		{
			admin: true,
			name:  "forget",
			re:    regexp.MustCompile(`(?i)^forget\s+(?P<match>.*)$`),
			f:     forget,
			help:  `["forget" pattern to forget] Unlearn messages within the last fifteen minutes containing the pattern to forget.`,
		},
		{
			admin: true,
			name:  "help",
			re:    regexp.MustCompile(`(?i)^(?:show\s+)?help(?:\s+on|\s+for)?\s+(?P<cmd>\S+)$`),
			f:     help,
			help:  `["help" command-name] Show help on a command. (I think you figured it out.)`,
		},
		{
			admin: true,
			name:  "invocation",
			re:    regexp.MustCompile(`(?i)^(?:show\s+)?invocation\s+(?:of\s+)?(?P<cmd>\S+)$`),
			f:     invocation,
			help:  `["invocation" command-name] Show the exact invocation regex for a command.`,
		},
		{
			admin: true,
			name:  "list",
			re:    regexp.MustCompile(`(?i)^(?:list\s+)?commands$`),
			f:     list,
			help:  `["list commands"] List all commands.`,
		},
		{
			admin: true,
			name:  "silence",
			re:    regexp.MustCompile(`(?i)^(?:be\s+quiet|shut\s+up|stfu)(?:\s+for\s+(?P<dur>(?:\d+[hms]){1,3}|an\s+h(?:ou)?r|\d+\s+h(?:ou)?rs?|a\s+min(?:ute)?|\d+\s+min(?:ute)?s?)|\s+until\s+(?P<until>tomorrow))?$`),
			f:     silence,
			help:  `["be quiet" ("for" 1h2m3s | "until" tomorrow)] Don't randomly speak or learn for a while.`,
		},
		{
			admin: true,
			name:  "unsilence",
			re:    regexp.MustCompile(`(?i)^you\s+(?:may|can)\s*(?:speak|talk|learn)(?:\s+again)?$`),
			f:     unsilence,
			help:  `["you may speak"] Disable an earlier silence command.`,
		},
		{
			admin: true,
			name:  "leave",
			re:    regexp.MustCompile(`(?i)^go\s+away[.!]*$`),
			f:     leave,
			help:  `["go away"] Leave the channel. The bot will return the next time it's booted.`,
		},
		{
			admin: true,
			name:  "too-active",
			re:    regexp.MustCompile(`(?i)^(?:you'?re?|you\s+are|u\s*r)\s+(?:too?|2)\s+active$`),
			f:     tooActive,
			help:  `["you're too active"] Reduce the random response rate.`,
		},
		{
			admin: true,
			name:  "set-prob",
			re:    regexp.MustCompile(`(?i)^(?:set\s+)?(?:(?:rand(?:om)\s+)?response\s+)?(?:prob(?:ability)?|rate)\s+(?:to\s+)?(?P<prob>[0-9.]+)(?P<percent>%)?$`),
			f:     setProb,
			help:  `["set response probability to" prob] Set the random response rate to a particular value.`,
		},
		{
			admin: true, regular: true,
			name: "talk",
			re:   regexp.MustCompile(`(?i)^(?:say|speak|talk|generate)(?:\s+something)?(?:\s+with|\s+meme|\s+raid\s+message)?(?:\s+(?P<chain>.+))?$`),
			f:    talk,
			help: `["say|speak|talk|generate with" starting chain)] Speak! Messages generated this way start with the given starting chain.`,
		},
		{
			admin: true, regular: true,
			name: "uwu",
			re:   regexp.MustCompile(`(?i)^uwu$`),
			f:    uwu,
			help: `["uwu"] Speak! Messages genyewated this way awe uwu.`,
		},
		// talk-catchall MUST be last
		{
			admin: true, regular: true,
			name: "talk-catchall",
			re:   regexp.MustCompile(``),
			f:    talkCatchall,
			help: `Speak! Respond to being directly addressed.`,
		},
	}
}

func findcmd(name string) *command {
	for _, cmd := range all {
		if strings.EqualFold(name, cmd.name) {
			return cmd
		}
	}
	return nil
}

// selsend sends a message with context cancellation.
func selsend(ctx context.Context, send chan<- irc.Message, msg irc.Message) {
	select {
	case <-ctx.Done(): // do nothing
	case send <- msg: // do nothing
	}
}