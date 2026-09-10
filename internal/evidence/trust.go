package evidence

import (
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TrustLink connects a claim — by digest of its Statement — to one of its
// evidence digests, carrying the claim's Confidence.
type TrustLink struct {
	From       string  `json:"from"`       // claim digest: Digest(claim.Statement)
	To         string  `json:"to"`         // evidence digest: Digest(evidence.Content)
	Confidence float64 `json:"confidence"` // the claim's Confidence
}

// TrustChainSection is the trust chain for a set of claims: the claims
// themselves plus the claim-to-evidence links that ground them.
type TrustChainSection struct {
	Claims []domain.Claim `json:"claims"`
	Links  []TrustLink    `json:"links"`
}

// BuildTrustChain links each claim (From = Digest(statement)) to each of its
// evidence digests (To = Digest of each Evidence.Content) with the claim's
// Confidence. Claims without evidence produce no links but are still listed
// in Claims.
func BuildTrustChain(claims []domain.Claim) *TrustChainSection {
	tc := &TrustChainSection{
		Claims: append([]domain.Claim(nil), claims...),
	}
	for _, c := range claims {
		from := Digest(c.Statement)
		for _, e := range c.Evidence {
			tc.Links = append(tc.Links, TrustLink{
				From:       from,
				To:         Digest(e.Content),
				Confidence: c.Confidence,
			})
		}
	}
	return tc
}

// VerifyTrustChain validates chain integrity, returning the first error:
//   - a link whose From does not match Digest(statement) of any listed claim
//   - a link whose To is not among the digests of that claim's evidence
//   - a Confidence outside [0,1]
//
// nil when valid.
func VerifyTrustChain(tc *TrustChainSection) error {
	if tc == nil {
		return fmt.Errorf("trust chain is nil")
	}
	byFrom := make(map[string]domain.Claim, len(tc.Claims))
	for _, c := range tc.Claims {
		byFrom[Digest(c.Statement)] = c
	}
	for _, l := range tc.Links {
		claim, ok := byFrom[l.From]
		if !ok {
			return fmt.Errorf("trust link from %q does not match any claim", l.From)
		}
		found := false
		for _, e := range claim.Evidence {
			if Digest(e.Content) == l.To {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("trust link to %q is not evidence of claim %q", l.To, claim.Statement)
		}
		if l.Confidence < 0 || l.Confidence > 1 {
			return fmt.Errorf("trust link confidence %v out of range [0,1]", l.Confidence)
		}
	}
	return nil
}
