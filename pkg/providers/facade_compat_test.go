package providers

import (
	oauthprovider "github.com/biaobiaobiao108/gogoclaw/pkg/providers/oauth"
	"testing"
)

func TestAntigravityFacadeSignaturesRemainAvailable(t *testing.T) {
	var _ func(string) (string, error) = FetchAntigravityProjectID
	var _ func(string, string) ([]AntigravityModelInfo, error) = FetchAntigravityModels
	var _ AntigravityModelInfo = oauthprovider.AntigravityModelInfo{}
}
