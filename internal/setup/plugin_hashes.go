package setup

import (
	"crypto/sha256"
	"encoding/hex"
)

// pluginHash returns the SHA-256 of a plugin file's bytes.
func pluginHash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// isShippedPluginVersion reports whether the bytes are a kern-deployed
// version of the opencode plugin: the current embedded asset or any
// previously shipped one (see shippedPluginHashes).
func isShippedPluginVersion(b []byte) bool {
	return shippedPluginHashes[pluginHash(b)]
}

// Recovered from git history (every committed // blob of assets/plugin/kern.ts) — regenerate with:
//
//	for c in $(git log --format=%H -- internal/setup/assets/plugin/kern.ts); do
//	  git show $c:internal/setup/assets/plugin/kern.ts | shasum -a 256
//	done | sort -u
//
// Purpose (QA Pick #9, F-DR1): "customized" detection was a byte-diff against
// the CURRENT asset, so a copy deployed by an older kern and never hand-edited
// was indistinguishable from user customization — kern doctor flagged it stale
// and prescribed `kern setup --global`, which then refused to fix it. A copy
// whose hash matches a SHIPPED version is kern's own deployment (safe to
// update); only a hash matching NO shipped version is a user edit (never
// overwritten).
//
// MAINTENANCE: when the plugin changes, TestShippedPluginHashRegistryCurrent
// fails until the new asset's hash is appended here (the test prints it).
var shippedPluginHashes = map[string]bool{
	"02863a712c698fc60032573841ad78dec1b68ed384ba9d08e78a2546c903313e": true,
	"0b311d40bb44d3cc026cc45e02dae7c8402bb1faf8917e3d3bad0ba02af37430": true,
	"1f75d756f163fd2e571ea8e3baaeb626a885faa4ba9032beae2a7485c62369a3": true,
	"22424768056f7b7eda7c6a66240e9d03c532ff7681bb39bdad7c1576744a21bc": true,
	"2d6da6564ab4ebeb7862446864ac016c21ab638b5524800157043a63e87ef48b": true,
	"2df9fb0cb6e0e44b91c225cab64616c66c2b8afdced30f77d5ec7a24dadf50b4": true,
	"2efa51a6adbf786b24d340a60c280268157cf58614ec2a807e6ba366cdfcc86a": true,
	"2f7e12ca4abd198f11aa6c4fc88732cece351587b73f97a358b1b00ba85a51a1": true,
	"2fe9b995508b16817eedba875f9fcb470e5357b9389d7744f4543d40a3779fb4": true,
	"3c8b0f54c498a7a81fb45bc8fdd9f044ce6203be4c320e372b411fbe9613d8c4": true,
	"418eb05843a0a34cfd8759da180eeffe9891ebd1a81439f1660001c35b1e74ab": true,
	"4515962cab18c86e02877636f95e901d77a757681985c6632bd20323d57418f0": true,
	"4ca936e9875db98b5ace9fc1913dfca70861d66d6943d842e317c3b626da7c8f": true,
	"4e011ffb8e7913e6502884eea3d0f028e131dc2df546d0efb2dde486e2de9d51": true,
	"4e3fa28220eea5c7e1ae86d332825f1df2ac682d4f28309960cf04bcdad832dd": true,
	"4f9e62f7f7d2c8d78d5d89c7aab30c009fda3647664493228b96d84ccdc36ea7": true,
	"4fac57b1ffb1aec539ff8d5aa1a1ac63de37e96361f0cf6e53e795ccc145485e": true,
	"4fff89930889d659a405ef56c30660a580c6cd9d8484805c69aaa9c144718107": true,
	"58458b7f5461bb62df0431fad959c2fab0150eab72fa1b98c05903c1b9360070": true,
	"5f6af278c140fd02756ce946f51c71e309dfe7cc5d6a6065dfe5151d5c26fbba": true,
	"796bf0f87e5e4a8e4eeef61005119698df979ce4ededd781912584b2efc4e1b3": true,
	"79f1780d6c0f5e36eb82a35fb5a9a703df0cd91cb3ffd24043f35bf14ac03d27": true,
	"7e8e04b3a27659d3fba902c2275250983491eb66090c4c36449165204160befd": true,
	"7f2303b2fab743487a775fe5838f70f29523097cdb540ae623e5a69828bb0771": true,
	"9278e87f50b2e3675599dae2ace14aea9c07ee1f241c2e78bcc3f12e3eec243f": true,
	"9900f75adc60b57dc6578dfd255e603d408bf4de8e9de0300bdb7015e86c09e7": true,
	"994f462c96bc4c4b21f90d5a59d04ca33862e139201e9bb2b403f48f9075c128": true,
	"acdc34189e7e6092434edce0fc384155433302759f2a0fbabf87a6d78b512d70": true,
	"b02583abe3248785a332c8f460d47ee93583936932777e713f8cf69c02b30d17": true,
	"b6d86a932766a2e2e91029d1cc235f17d157014a411fc89598bfea55ee567513": true,
	"bf54e46d8436afa43838fc628b6678c9e800e28f28d191416f26998c98fb5e28": true,
	"da96e1be762aadf1a6c3830b368777f627ec8fe0b686a213310bbaf8da68c08f": true,
	"dc815fc5967097ab81045b1963ce4485b0202f1700af01f99d0dc8e8462f7714": true,
	"e14b14cf5e7daf2da3b27d1009671cca155f940d28c06b7e80ff3a9646f0d79b": true,
	"f0f28a37c7d7c3bde68def122c8adb77c7beaea8d1f1fd91de9759c1818b8437": true,
	"f7e6f783b7d869b8c661d07e4959395a9342245864059fddcfb4736fd82c6a36": true,
	"f8b3abd9e608ef3342e4fb2a203649db788390aa5a2c37ce0824861e1b210e52": true,
	"688e74ae676c98218ed442643607bf8eb067f8cd955ac750d904e877841b5798": true,
}
