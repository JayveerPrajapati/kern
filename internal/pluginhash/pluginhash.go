// Package pluginhash holds the registry of SHA-256 hashes of every opencode
// plugin version kern has ever shipped, so an installed copy can be told apart
// from a user edit.
package pluginhash

import (
	"crypto/sha256"
	"encoding/hex"
)

// Hash returns the SHA-256 of a plugin file's bytes.
func Hash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// IsShipped reports whether the bytes are a kern-deployed version of the
// opencode plugin: the current embedded asset or any previously shipped one.
func IsShipped(b []byte) bool {
	return shipped[Hash(b)]
}

// Recovered from git history (every committed blob of
// internal/agentwire/assets/plugin/kern.ts) — regenerate with:
//
//	for c in $(git log --format=%H -- internal/agentwire/assets/plugin/kern.ts); do
//	  git show $c:internal/agentwire/assets/plugin/kern.ts | shasum -a 256
//	done | sort -u
//
// Purpose (QA Pick #9, F-DR1): "customized" detection was a byte-diff against
// the CURRENT asset, so a copy deployed by an older kern and never hand-edited
// was indistinguishable from user customization — kern doctor flagged it stale
// and prescribed `kern setup --global`, which then refused to fix it. A copy
// whose hash matches a SHIPPED version is kern's own deployment (updated
// directly); a hash matching NO shipped version is refreshed under a backup
// (BackupDivergentPlugin) so the remedy converges while user edits stay
// recoverable.
//
// MAINTENANCE: when the plugin changes, TestShippedPluginHashRegistryCurrent
// fails until the new asset's hash is appended here (the test prints it).
var shipped = map[string]bool{
	"b0eaf50d348cab18122f91df6663af756de7c34b541ece3b5a0960c4173a9cd2": true,
	"da4c32e6760b133fc48042bdcbbdd9ee96986814a7527d8da702c8c1a59a46aa": true,
	"9b1359de178434aefd253fb8121a09c0ccdd6630cd1335eef4a0fe78b506e696": true,
	"e1ec747ba70593d4db8aac8ae157995c04bf0db00d816ab60ab939475f8e6846": true,
	"c6046a419cc3e6db4cd897ae56710c1d5c3f2840e3e35b73fc2906d63bea93a5": true,
	"612a520ec6b06c0185ecd903d30c179581b75d67e85792acac543660dcb8a6d0": true,
	"cad56f613ea552c7d65b9fe40ce12373f6a822f5b47e5c6171348fe88cee33a7": true,
	"f967da0ae9d0bc2b2bfc8a6a2ec6543f5a4c85f0d45a56d03f5ae8ed506d0012": true,
	"7e3f3535b292d8de2f71d6dc8398b6815287696414d63c00fbcc7f19e5b208f2": true,
	"e09e540b3a9a951f15fc763f47450e93af8a7024786d887b27a5e7d8203ccd83": true,
	"038adf6b9f95302a2ea1b0ab6c6b240042cb2bfcc5e252f50de2e274a6926217": true,
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
	"3959360f44448f8678699c3e4db3bf8a01a7aa764da6093ae20abe88b0b50a31": true,
	"3c8b0f54c498a7a81fb45bc8fdd9f044ce6203be4c320e372b411fbe9613d8c4": true,
	"418eb05843a0a34cfd8759da180eeffe9891ebd1a81439f1660001c35b1e74ab": true,
	"428db58857cc03f973cc40b48a545ce7296f631cab17f1e2d4941e16cc76fd4c": true,
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
	"ac0a7d8ba2434b33ec96f2ada10110028256b170343bcf507a43f4eb4f74f0f5": true,
	"64a4339c06377135e35006933f821923917d682d4e5c68e10185cfa93c461cda": true,
	"8a04d5cea5207af6540566aa176e59004828598fa7677d3e22562824c787ed98": true,
	"8ada8e7b4f49694be8e819280b0850649665afe388a7ec79ee9afa441644e10d": true,
	"55765df37d31062060862988f411717e472cd17a0f3d6152eb099a4d430b7168": true,
	"a0a8122cc78279ded425e370d5ff0756da7ab538fccca9ad8517c044c72745e7": true,
	"675fe88a8712377e0e41b1b8aae4a9df90953e06d7e6f6812930efce1547ecf5": true,
	"d0993ff83e4d067cff79f7e3f2bcce6914e55e5260568e0d3dd4e699fc182775": true,
	"4b20013316891d9a91c375dc582f6ed7dc2d396e8ee610ff5f09aa602b891c28": true,
	"f41ef149a4c10d60568b3f584770ee1a8aff1902a63175aa2eb4080da5a528d7": true,
	"1d83d92a6fb9150056c0f123bd87c6cba383837ee5e901d79ec6e2483ed6c9ed": true,
	"9ad35861896c5c24e98840e3a083c140d0e6be34dfea837f9f7e2dc831ea8aeb": true,
	"d6235174271fd1fdc79673274c7178238447355e33693159f4aaba373fbec5d8": true,
	"b7d43d0399f363c01ffdc4fb1165eb1dbc7516913a61caf1bd946b816559a20f": true,
	"a33a39fca5b98fd0d53cef583c9c63fddc30ea48f65fab17d34b22c1d396fdfb": true,
	"2ea086de97e9519012bbb7d6cd3db7a4cf7528d5f29de1e218dffd3732bfafb4": true,
	"aeeddb90d7eccf931410bfcf64cafd5286783ba21831f321ace8be48be93d5c0": true,
	"da8493f794a8b3316b9e5146be16f7225b088a37b99fc8a9333d58c5879e90e1": true,
	"6fa203b0f360d0cf40688111b6bf0e12e89e08418767ea88d7d1852f9a9899c4": true,
	"4e3e6a9a5b3ba97a898926d536a9d8711885be5965a41a72538da9c7c131ed69": true,
	"d69526bbc7d9742ff5ca2fbf63832bae2770191ebc4ae72d963cf41bbf0a979d": true,
	"7e1e860fa6d452fd45690a191a41aac436ad602d54e27b6db38321ea2c6f4d8d": true,
	"6dabee7b8a914ef833566fb7a20e14be67120dd5063e8538abcbb09a669c521a": true,
	"6f032853f93ae954f944dcf4b33a241ff48e7ba196fdc9d727481835e1c85992": true,
	"eee25e43622f99f3ad1bd40f3c7af527a41cf11c4b1ca85aac1dfa76d3a9c8df": true,
	"53d3ceb48e90b6560bca8df3ce76eedc55a0fb31ae217d908eaf64e898de2dde": true,
	"9310f76b2d086b77bae4c96f1b283edda7a8c7b7c6c502ca4b21c8574aa3ac9b": true,
	"5c0e65ec3ef02ec9d4eda5f21a41f201f811fcfd9d65377d184b99c8feb7c2b3": true, // P0.3: host-neutral grep-arm reason (raw=true advice removed)
	"45f38c4bd1df954c5a63fdf793c2aad254ed55df8d14a35f159a37b680053a8b": true, // P0.6: scoped grep (named file/dir target) serves tier 3, never the repo-wide symbol search
	"343998d9f51c0e229d40027e19a67fc0bf6d6bbe241c773b629b6818dfa88374": true, // P1.4: Ask Protocol — GATE arm consults guard grant, deny teaches ask-wait-retry
}
