package tokenize

import (
	"sync"
	"testing"
)

// estGeneric mirrors the pre-hot-swap behavior of Count/CountKind.
func estGeneric(s string) int      { return (Estimator{Kind: KindGeneric}).Count(s) }
func estKind(s string, k Kind) int { return Estimator{Kind: k}.Count(s) }

func TestDefaultCounterIsEstimator(t *testing.T) {
	ResetDefault()
	defer ResetDefault()
	if _, ok := Default().(Estimator); !ok {
		t.Fatalf("default counter = %T, want Estimator", Default())
	}
	// Behavior must be identical to the pre-hot-swap functions.
	for _, s := range []string{"hello world", "code: x := y + 1", "log: 2026-09-01 ERROR boom"} {
		if got, want := Count(s), estGeneric(s); got != want {
			t.Errorf("Count(%q) = %d, want %d", s, got, want)
		}
		if got, want := CountKind(s, KindCode), estKind(s, KindCode); got != want {
			t.Errorf("CountKind(%q, code) = %d, want %d", s, got, want)
		}
	}
}

func TestSetDefaultSwapsCounter(t *testing.T) {
	ResetDefault()
	defer ResetDefault()
	c, err := NewCl100kCounter()
	if err != nil {
		t.Fatal(err)
	}
	SetDefault(c)
	if got := Count("hello world"); got != c.Count("hello world") {
		t.Errorf("Count after SetDefault = %d, want %d", got, c.Count("hello world"))
	}
	// Kind is ignored by exact counters (it must not change the count).
	if got := CountKind("hello world", KindLog); got != c.Count("hello world") {
		t.Errorf("CountKind after SetDefault = %d, want %d", got, c.Count("hello world"))
	}
	SetDefault(nil) // resets to Estimator
	if got, want := Count("hello world"), estGeneric("hello world"); got != want {
		t.Errorf("Count after SetDefault(nil) = %d, want %d", got, want)
	}
}

func TestInitFromEnvSelection(t *testing.T) {
	ResetDefault()
	defer ResetDefault()
	cases := []struct {
		name      string
		tokenizer string
		model     string
		wantName  string // "", "bpe", "cl100k_base", "o200k_base"
	}{
		{"unset", "", "", ""},
		{"explicit estimator", "estimator", "gpt-4o", ""},
		{"bpe", "bpe", "", "bpe"},
		{"cl100k", "cl100k", "", "cl100k_base"},
		{"cl100k_base alias", "cl100k_base", "", "cl100k_base"},
		{"o200k", "o200k", "", "o200k_base"},
		{"o200k_base alias", "O200K_BASE", "", "o200k_base"},
		{"unknown falls back", "nope", "", ""},
		{"model gpt-4o", "", "gpt-4o-mini", "o200k_base"},
		{"model chatgpt-4o", "", "chatgpt-4o-latest", "o200k_base"},
		{"model o1", "", "o1-preview", "o200k_base"},
		{"model gpt-4", "", "gpt-4-turbo", "cl100k_base"},
		{"model gpt-3.5", "", "gpt-3.5-turbo", "cl100k_base"},
		{"model llama stays estimator", "", "llama3.2", ""},
		{"tokenizer beats model", "estimator", "gpt-4o", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KERN_TOKENIZER", tc.tokenizer)
			t.Setenv("KERN_MODEL", tc.model)
			InitFromEnv()
			defer ResetDefault()
			switch tc.wantName {
			case "":
				if _, ok := Default().(Estimator); !ok {
					t.Fatalf("Default() = %T, want Estimator", Default())
				}
			case "bpe":
				if _, ok := Default().(*BPECounter); !ok {
					t.Fatalf("Default() = %T, want *BPECounter", Default())
				}
			default:
				tk, ok := Default().(*TiktokenCounter)
				if !ok {
					t.Fatalf("Default() = %T, want *TiktokenCounter", Default())
				}
				if tk.Name() != tc.wantName {
					t.Fatalf("encoding = %q, want %q", tk.Name(), tc.wantName)
				}
			}
		})
	}
}

func TestTiktokenFixturesCl100k(t *testing.T) {
	c, err := NewCl100kCounter()
	if err != nil {
		t.Fatal(err)
	}
	if got := c.VocabSize(); got != 100256 {
		t.Errorf("cl100k vocab = %d entries, want 100256", got)
	}
	for _, f := range tiktokenFixtures {
		if got := c.Count(f.text); got != f.cl100k {
			t.Errorf("cl100k Count(%q) = %d, want %d", f.text, got, f.cl100k)
		}
	}
}

func TestTiktokenFixturesO200k(t *testing.T) {
	c, err := NewO200kCounter()
	if err != nil {
		t.Fatal(err)
	}
	if got := c.VocabSize(); got != 199998 {
		t.Errorf("o200k vocab = %d entries, want 199998", got)
	}
	for _, f := range tiktokenFixtures {
		if got := c.Count(f.text); got != f.o200k {
			t.Errorf("o200k Count(%q) = %d, want %d", f.text, got, f.o200k)
		}
	}
}

func TestTiktokenSpecialTokens(t *testing.T) {
	// Built by concatenation: the literal special marker must not appear
	// verbatim in this file (some transports strip it).
	sp := "<|endoftext" + "|>"
	c, _ := NewCl100kCounter()
	o, _ := NewO200kCounter()
	// The special counts as one token, splitting the text around it.
	if got, want := c.Count("hello "+sp+" world"), c.Count("hello ")+1+c.Count(" world"); got != want {
		t.Errorf("cl100k special split: got %d, want %d", got, want)
	}
	if got, want := o.Count("a "+sp+" b"), o.Count("a ")+1+o.Count(" b"); got != want {
		t.Errorf("o200k special split: got %d, want %d", got, want)
	}
	// Adjacent specials: two tokens.
	if got := c.Count(sp + sp); got != 2 {
		t.Errorf("cl100k adjacent specials = %d, want 2", got)
	}
	if got := o.Count(sp + sp); got != 2 {
		t.Errorf("o200k adjacent specials = %d, want 2", got)
	}
}

func TestTiktokenEmptyAndEdge(t *testing.T) {
	c, _ := NewCl100kCounter()
	if got := c.Count(""); got != 0 {
		t.Errorf("empty = %d, want 0", got)
	}
	if got := c.Count("a"); got != 1 {
		t.Errorf("single rune = %d, want 1", got)
	}
	// Invalid UTF-8 must not panic; it degrades to "other" chars.
	_ = c.Count(string([]byte{0xff, 0xfe, 'a'}))
}

func TestDefaultCounterConcurrent(t *testing.T) {
	ResetDefault()
	defer ResetDefault()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				Count("concurrent counting stress")
				CountKind("concurrent counting stress", KindCode)
			}
			if i == 0 {
				c, _ := NewCl100kCounter()
				SetDefault(c)
			}
		}(i)
	}
	wg.Wait()
}
