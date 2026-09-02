package tokenize

// tiktokenFixtures is ground truth generated with tiktoken 0.14.0
// (Rust reference engine) against the same rank tables embedded in
// data/. Each entry is the exact encode() length for both encodings.
// Regenerate by pip-installing tiktoken, building both encodings from
// internal/tokenize/data/*.tiktoken.gz with the 0.14.0 pattern strings,
// and emitting len(enc.encode(text, allowed_special="all")) per entry;
// do not hand-edit the expected counts.
var tiktokenFixtures = []struct {
	text   string
	cl100k int
	o200k  int
}{
	{"hello world", 2, 2},
	{"tiktoken is great!", 6, 6},
	{"Hello, world!", 4, 4},
	{"  double  spaces  ", 5, 5},
	{"leading space", 2, 2},
	{"trailing space ", 4, 4},
	{"multiple   spaces between", 4, 4},
	{"line1\nline2\nline3", 8, 8},
	{"spaces then newline   \n  then more", 7, 7},
	{"\r\nCRLF\r\nlines\r\n", 6, 6},
	{"don't stop believin'", 6, 5},
	{"IT'S UPPER CASE 'LL", 6, 7},
	{"we've they're you'll he'd it's She'S I'M", 14, 10},
	{"digits 123456789 and 42 and 007", 11, 11},
	{"punct !!! ??? ... — dash", 7, 7},
	{"unicode: café naïve 日本語 中文 한국어 🚀 emoji", 19, 13},
	{"func main() { fmt.Println(\"hello\") }", 10, 10},
	{"x := a.b[c] + d*(e-f)", 12, 12},
	{"CamelCaseIdentifier and snake_case_id", 8, 7},
	{"  \n  x", 3, 3},
	{"\n\n\n", 1, 1},
	{"    ", 1, 1},
	{"a", 1, 1},
	{"word\n\n\nword", 3, 3},
	{"tab\there", 3, 3},
	{"mixed\t \n \twhitespace", 6, 6},
	{"  Hello   world!  \n\n  gpt-4o  ", 13, 13},
	{"NASA and FBI and iPhone", 5, 6},
	{" ’unicode apostrophe’ test ", 7, 7},
	{"https://example.com/path?query=1&x=2#frag", 15, 15},
	{"SGVsbG8gd29ybGQgdGhpcyBpcyBhIGJhc2U2NCBibG9iIHRvIHN0cmVzcyB0aGUgQlBFIG1lcmdlIGxvb3A=", 61, 55},
	{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 10, 10},
	{"The quick brown fox jumps over the lazy dog. The quick brown fox jumps over the lazy dog. The quick brown fox jumps over the lazy dog. ", 31, 31},
	{"type Engine struct {\n\tix *Index\n\tmu sync.RWMutex\n}\n\nfunc (e *Engine) Build(root string) error {\n\treturn nil\n}", 33, 33},
	{"error: connection refused (errno 61); retrying in 500ms", 15, 15},
	{"中文数字混排123abc", 8, 6},
	{"ẞẞ uppercase sharp s", 7, 7},
	{"ʰ modifier letter", 4, 4},
	{" e'", 2, 2},
	{"'s alone", 2, 2},
	{"\r\r weird CR", 4, 3},
	{" \t \n mixed trailing   \t ", 6, 6},
	{"a\n  ", 3, 3},
	{"abc \n ", 3, 3},
	{"paths a/b/c and //double//slash", 9, 9},
	{"IT'S NASA'S DON'T", 6, 5},
	{"A/B/C", 3, 3},
	{"1/2/3", 5, 5},
	{"x  y", 3, 3},
	{"word   ", 2, 2},
	{"end with newline\n", 4, 4},
	{"hello  world", 3, 3},
}
