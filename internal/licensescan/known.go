// Package licensescan resolves a Go module's license offline: a deterministic
// known-module table plus a read of the local module cache. It never runs the
// go tool and never uses the network.
package licensescan

import "strings"

// knownLicenses maps famous module paths / path prefixes to their well-known
// license. Longest-prefix ordering matters (tree-sitter-javascript before
// tree-sitter-java). Anything absent stays "unknown" rather than guessed.
var knownLicenses = []struct {
	prefix string
	lic    string
}{
	{"golang.org/x/", "BSD-3-Clause"},
	{"google.golang.org/", "Apache-2.0"},
	{"github.com/aws/", "Apache-2.0"},
	{"github.com/docker/", "Apache-2.0"},
	{"github.com/google/uuid", "BSD-3-Clause"},
	{"github.com/stretchr/testify", "MIT"},
	{"github.com/sirupsen/logrus", "MIT"},
	{"github.com/spf13/cobra", "Apache-2.0"},
	{"github.com/spf13/pflag", "BSD-3-Clause"},
	{"github.com/spf13/viper", "MIT"},
	{"github.com/pkg/errors", "BSD-2-Clause"},
	{"github.com/prometheus/client_golang", "Apache-2.0"},
	{"github.com/gorilla/mux", "BSD-3-Clause"},
	{"github.com/golang/protobuf", "BSD-3-Clause"},
	{"github.com/gin-gonic/gin", "MIT"},
	{"github.com/labstack/echo", "MIT"},
	{"gopkg.in/yaml.v3", "MIT"},
	{"gopkg.in/yaml.v2", "Apache-2.0"},
	// tree-sitter grammar family — MIT per the LICENSE files in the Go
	// module cache (each repo ships the standard MIT text; longest-prefix
	// first so e.g. tree-sitter-javascript/tree-sitter-css are not shadowed
	// by tree-sitter-java/tree-sitter-c).
	{"github.com/UserNobody14/tree-sitter-dart", "MIT"},
	{"github.com/tree-sitter/go-tree-sitter", "MIT"},
	{"github.com/tree-sitter/tree-sitter-typescript", "MIT"},
	{"github.com/tree-sitter/tree-sitter-javascript", "MIT"},
	{"github.com/tree-sitter/tree-sitter-java", "MIT"},
	{"github.com/tree-sitter/tree-sitter-python", "MIT"},
	{"github.com/tree-sitter/tree-sitter-rust", "MIT"},
	{"github.com/tree-sitter/tree-sitter-ruby", "MIT"},
	{"github.com/tree-sitter/tree-sitter-php", "MIT"},
	{"github.com/tree-sitter/tree-sitter-css", "MIT"},
	{"github.com/tree-sitter/tree-sitter-cpp", "MIT"},
	{"github.com/tree-sitter/tree-sitter-c", "MIT"},
	{"github.com/tree-sitter/tree-sitter-go", "MIT"},
	{"github.com/tree-sitter/tree-sitter-bash", "MIT"},
	// modernc.org family — BSD-3-Clause per the module-cache LICENSE files
	// (modernc.org/token and ledongthuc/pdf carry the Go Authors BSD text).
	{"github.com/ledongthuc/pdf", "BSD-3-Clause"},
	{"modernc.org/sqlite", "BSD-3-Clause"},
	{"modernc.org/gc/v3", "BSD-3-Clause"},
	{"modernc.org/libc", "BSD-3-Clause"},
	{"modernc.org/mathutil", "BSD-3-Clause"},
	{"modernc.org/memory", "BSD-3-Clause"},
	{"modernc.org/strutil", "BSD-3-Clause"},
	{"modernc.org/token", "BSD-3-Clause"},
	// Miscellaneous well-known modules read from the module cache.
	{"github.com/dustin/go-humanize", "MIT"},
	{"github.com/hashicorp/golang-lru/v2", "MPL-2.0"},
	{"github.com/mattn/go-isatty", "MIT"},
	{"github.com/mattn/go-pointer", "MIT"},
	{"github.com/mattn/go-colorable", "MIT"},
	{"github.com/ncruces/go-strftime", "MIT"},
	{"github.com/remyoudompheng/bigfft", "BSD-3-Clause"},
	{"cel.dev/cel-go", "Apache-2.0"},
	{"github.com/confluentinc/confluent-kafka-go", "Apache-2.0"},
	{"github.com/quic-go/quic-go", "MIT"},
	{"github.com/rs/zerolog", "MIT"},
	{"github.com/clbanning/mxj", "MIT"},
	{"github.com/swaggo/gin-swagger", "MIT"},
	{"github.com/swaggo/files", "MIT"},
	{"github.com/swaggo/swag", "MIT"},
	{"github.com/yalp/jsonpath", "BSD-3-Clause"},
	{"github.com/bytedance/", "Apache-2.0"},
	{"github.com/cloudwego/", "Apache-2.0"},
	{"github.com/go-openapi/", "Apache-2.0"},
	{"github.com/go-playground/", "MIT"},
	{"github.com/goccy/", "MIT"},
	{"github.com/json-iterator/", "MIT"},
	{"github.com/klauspost/cpuid/", "MIT"},
	{"github.com/leodido/go-urn", "MIT"},
	{"github.com/modern-go/", "Apache-2.0"},
	{"github.com/pelletier/", "MIT"},
	{"github.com/pmezard/go-difflib", "BSD-3-Clause"},
	{"github.com/twitchyliquid64/golang-asm", "BSD-3-Clause"},
	{"github.com/ugorji/", "MIT"},
	{"github.com/gin-contrib/", "MIT"},
	{"github.com/gabriel-vasile/mimetype", "MIT"},
	{"github.com/antlr4-go/antlr/", "BSD-3-Clause"},
	{"github.com/KyleBanks/depth", "MIT"},
	{"github.com/davecgh/go-spew", "ISC"},
	{"gopkg.in/natefinch/lumberjack.v2", "MIT"},
	{"gopkg.in/xmlpath.v2", "LGPL-3.0"},
	{"go.mongodb.org/", "Apache-2.0"},
}

// Known resolves a module path against the known-license table: the first
// matching entry (longest-prefix sensitive via ordering) or "".
func Known(path string) string {
	for _, k := range knownLicenses {
		if strings.HasPrefix(path, k.prefix) {
			return k.lic
		}
	}
	return ""
}
