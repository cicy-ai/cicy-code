package main

import "testing"

// 配置类 503(定价/供应商未配置、team token 无效)重试十次只会产生十条一样的失败,
// 还会往微信推十条 ——判定为终态,不重试。
func TestCicyGatewayErrorIsTerminal(t *testing.T) {
	for _, body := range []string{
		`{"success":false,"error":"model_pricing_not_configured"}`,
		`{"success":false,"error":"provider_not_configured"}`,
		`{"success":false,"error":"invalid_team_token"}`,
	} {
		if !cicyGatewayErrorIsTerminal([]byte(body)) {
			t.Fatalf("%s should be terminal", body)
		}
	}
	for _, body := range []string{
		`{"success":false,"error":"upstream_timeout"}`,
		`overloaded`,
		``,
	} {
		if cicyGatewayErrorIsTerminal([]byte(body)) {
			t.Fatalf("%q must stay retryable", body)
		}
	}
}
