package show

import (
	"slices"
	"strings"

	"github.com/adammiribyan/superfind/internal/agent"
)

const trimEvery = 4 << 20

type group struct {
	text  string
	clean bool
}

func (g *group) cleaned() string {
	if !g.clean {
		g.text, g.clean = Mask(g.text), true
	}
	return g.text
}

type turn struct {
	role   string
	groups []group
	open   bool
	cut    bool
}

func keyOpen(text string) bool {
	if !strings.Contains(text, "PRIVATE KEY-----") {
		return false
	}
	found := privateKey.FindAllStringSubmatchIndex(text, -1)
	return len(found) > 0 && found[len(found)-1][2] == found[len(found)-1][3]
}

func (t *turn) add(text string) {
	text = agent.Printable(text)
	switch {
	case !t.open:
		t.groups = append(t.groups, group{text: text})
	case privateKeyEnd.MatchString(text):
		t.groups[len(t.groups)-1].text += "\n\n" + text
	default:
		return
	}
	t.open = keyOpen(t.groups[len(t.groups)-1].text)
}

func (t *turn) measure(limit int) int {
	if t.cut {
		return limit + 1
	}
	n := 2 * (len(t.groups) - 1)
	for i := len(t.groups) - 1; i >= 0 && n <= limit; i-- {
		n += len(t.groups[i].cleaned())
	}
	return n
}

func (t *turn) trimFront(limit int) int {
	n := 0
	for i := len(t.groups) - 1; i >= 0; i-- {
		if i < len(t.groups)-1 {
			n += 2
		}
		if i < len(t.groups)-1 || !t.open {
			n += len(t.groups[i].cleaned())
		}
		if n >= limit && i > 0 {
			t.groups = slices.Delete(t.groups, 0, i)
			t.cut = true
			return limit + 1
		}
	}
	if t.cut {
		return limit + 1
	}
	return n
}

func (t *turn) text() string {
	texts := make([]string, len(t.groups))
	for i := range t.groups {
		texts[i] = t.groups[i].cleaned()
	}
	return strings.Join(texts, "\n\n")
}

type keeper struct {
	opt          Options
	total        int
	turns        []*turn
	since, every int
}

func (k *keeper) say(role, text string) {
	if n := len(k.turns); n > 0 && k.turns[n-1].role == role {
		k.turns[n-1].add(text)
	} else {
		k.total++
		k.turns = append(k.turns, &turn{role: role})
		k.turns[n].add(text)
		if k.opt.Tail > 0 && len(k.turns) >= 2*k.opt.Tail {
			k.turns = slices.Delete(k.turns, 0, len(k.turns)-k.opt.Tail)
		}
	}
	if k.since += len(text); k.opt.MaxBytes > 0 && k.since >= k.every {
		k.trim()
		k.since = 0
	}
}

func (k *keeper) trim() {
	limit, first := k.opt.MaxBytes, len(k.turns)-1
	used := k.turns[first].trimFront(limit)
	for first > 0 && used <= limit {
		used += k.turns[first-1].measure(limit - used)
		if used <= limit {
			first--
		}
	}
	k.turns = slices.Delete(k.turns, 0, first)
}

func (k *keeper) conversation() Conversation {
	if k.opt.Tail > 0 && len(k.turns) > k.opt.Tail {
		k.turns = slices.Delete(k.turns, 0, len(k.turns)-k.opt.Tail)
	}
	c := Conversation{Total: k.total}
	limit, first := k.opt.MaxBytes, len(k.turns)
	if limit > 0 {
		used := 0
		for first > 0 {
			n := k.turns[first-1].measure(limit - used)
			if used+n > limit {
				break
			}
			used += n
			first--
		}
	} else {
		first = 0
	}
	kept := make([]Turn, 0, len(k.turns)-first)
	if first == len(k.turns) && first > 0 {
		first--
		t := k.turns[first]
		kept = append(kept, Turn{Role: t.role, Text: "…" + lastBytes(t.text(), limit), Cut: true})
		c.Truncated = true
	} else {
		for _, t := range k.turns[first:] {
			kept = append(kept, Turn{Role: t.role, Text: t.text()})
		}
	}
	window := 0
	if k.opt.Tail > 0 {
		window = max(k.total-k.opt.Tail, 0)
	}
	if k.total-len(k.turns)+first > window {
		c.Truncated = true
	}
	c.Turns = kept
	c.Omitted = c.Total - len(kept)
	return c
}

func Read(a *agent.Agent, path string, opt Options) (Conversation, error) {
	return read(a, path, opt, trimEvery)
}

func read(a *agent.Agent, path string, opt Options, every int) (Conversation, error) {
	own, peers := &keeper{opt: opt, every: every}, &keeper{opt: opt, every: every}
	t, err := agent.Listen(a, path, func(peer bool, role, text string) {
		if peer {
			peers.say(role, text)
		} else {
			own.say(role, text)
		}
	})
	if t.PeerDriven {
		return peers.conversation(), err
	}
	return own.conversation(), err
}
