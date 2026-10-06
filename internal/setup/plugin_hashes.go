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
	"52b283f3071fa4582650f80c89b3e455686e4d211b55e61540a04cd63ad47770": true,
	"72bb5dcf7ec2353fe2e5ab36a88977d6ed648bdbc17e2eb042fcdc267f5aaa10": true,
	"af7572ca21bd799f70b4fd206c3ec82322e34d05e28e61bda8c1c2676eb904dd": true,
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
	"df06623d0d1353c1d3c9cffa4b5854b2602a4ad510066f80ed5299bc31d738a8": true,
	"e14b14cf5e7daf2da3b27d1009671cca155f940d28c06b7e80ff3a9646f0d79b": true,
	"e15979806e0f15c952151666b0fcaa56631c169d7274d5926a21b2b53ea96ae6": true,
	"81b90fbb4343e1678be141a4fd241afbad5832769e932b386d20d23b709b25f6": true,
	"f0f28a37c7d7c3bde68def122c8adb77c7beaea8d1f1fd91de9759c1818b8437": true,
	"f7e6f783b7d869b8c661d07e4959395a9342245864059fddcfb4736fd82c6a36": true,
	"f8b3abd9e608ef3342e4fb2a203649db788390aa5a2c37ce0824861e1b210e52": true,
	"688e74ae676c98218ed442643607bf8eb067f8cd955ac750d904e877841b5798": true,
	"e21eb0564e4f69687ff6b2365caaaa3bdbbf057b8cfa4b5542c8298b317b3324": true,
	"35aade8f1f838e8887326232a1159aacfc0955f09bc8949f6072d6167f19ef8a": true,
	"4ee0cd6f9c096080a51476f096e49691eb9a25451440dad389eb91182fa2d196": true,
	"a192ce7d63d6e386b2c0a822cb0450b513e6a4d2ef8329f4aeb3bc133404c644": true,
	"fe7ba3c921807c20ba7350f784ac71cf207faeee87d472560830e18d00dd675f": true,
	"1a32bdfe0c00c27bf64de141998e966179d45b550850a557a4754dd19d5ced28": true,
	"fcfac8387abc2ab6e35fff8f1a228ec1243b1c1d9e579f70c517c2e31be9275f": true,
	"377067e90b27aa8b0a055f716358f8d9801b94302e46a864b760bb09a9bf20f4": true,
	"907de2cfd8e850dcb8aa9f84c7b614e6c0bbaa2e3537116cd6a49ccae77c0362": true,
	"c90a55445cc037b044b85bb33a9d460bed521383399d0830e8e02b9b12239d7d": true,
	"6bff927fc628ae6cabdbb01b9ed26db2a5f834937974d39a00e35f73482015a4": true,
	"027758c462033267be9c9c88f646fd5cb3f2bdc01fc1a7c71455543974be1577": true,
	"4d2fc6ba0acab4f31cd5d6753f5090e8666c4f88cef5e2439f144bbb7a30c6fc": true,
	"8c83e5d46f1e3f703c6189de29fae53c1d17c0086234c031169b86c641f2f9f7": true,
	"e868c09061822ce87520c183680756abaafecf04a1f36c52702f9320e5192062": true,
	"6e62240cda7d71ed6f61452f504a26cd53b7d8cdd6cfa09dadb8f12da30990fb": true,
	"138e8dae383f6c3b0c627d795cea11573bf0812538fd7d7cd3ea8893b47eab05": true,
	"4ed71167c0f952152c79fa64a7ecb71ebb92ec7a8b849bd39cd622aeeec84904": true,
	"9c3b0cd08e66806f1b911f4327d46adcb929183b3e68828224256fa246748bc1": true,
	"f8296a8906beb439ebd415eeeb9a49a49c8b13c88279b4a44f96e7ff4d85cc13": true,
	"ff8467cbdd112d221c2ed581781a02f2b086866e13e7c8a3966be3bd0de88b63": true,
	"91dae08d67d76d302bf493418856eaea669c4b92e348ca2c279293f063e15e5e": true,
	"2c0e1253f307cc33b81b4498319e5f6dd8942415ca0f99f61405223f95875237": true,
	"52d0f6aa351838c469aa0119511ca308f7ee41b70578f17749529e32be78ab69": true,
	"b87b52f8a6f27e90d844e68b09685f92d6bdc915333c7b61a9f846892ce63659": true,
	"8bf698cf3d62bbe1ffaa164d1cb5bec1f8663278dced71d9db2b95fe450fce15": true,
	"2ea6d8b81be94e5cf12440fc6df5d65f600f91156515fbf96de3667716e595af": true,
}
