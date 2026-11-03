package gemini

import (
	"context"
	nethttp "net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	http "github.com/bogdanfinn/fhttp"
	"github.com/bogdanfinn/fhttp/cookiejar"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

func TestDoNavigationWithRedirects(t *testing.T) {
	// 模拟 Google 302 sorry/index 免检重定向握手链
	var step int
	var serverURL string

	testHandler := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		step++
		switch step {
		case 1:
			// Step 1: 首次访问 302 至 sorry
			w.Header().Set("Location", serverURL+"/sorry/index?continue="+url.QueryEscape(serverURL+"/app"))
			w.WriteHeader(nethttp.StatusFound)
		case 2:
			// Step 2: 访问 sorry 自动颁发 google_abuse 令牌并 302 回目标页
			abuseParam := "GOOGLE_ABUSE_EXEMPTION=ID=test1234:TM=123:C=>;+path=/;+domain=google.com"
			w.Header().Set("Location", serverURL+"/app?google_abuse="+url.QueryEscape(abuseParam))
			w.WriteHeader(nethttp.StatusFound)
		case 3:
			// Step 3: 带令牌请求，302 到最终页面并设置 Cookie
			w.Header().Set("Set-Cookie", "GOOGLE_ABUSE_EXEMPTION=ID=test1234; Path=/")
			w.Header().Set("Location", serverURL+"/app")
			w.WriteHeader(nethttp.StatusFound)
		case 4:
			// Step 4: 最终返回 200 OK 页面
			w.WriteHeader(nethttp.StatusOK)
			_, _ = w.Write([]byte(`{"cfb2h":"test-token","SNlM0e":"test-snl","TuX5cc":"zh-CN","FdrFJe":"123"}`))
		default:
			t.Errorf("Unexpected step %d", step)
		}
	})

	server := httptest.NewServer(testHandler)
	defer server.Close()
	serverURL = server.URL

	jar, _ := cookiejar.New(nil)
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(),
		tls_client.WithClientProfile(profiles.Chrome_152),
		tls_client.WithNotFollowRedirects(),
		tls_client.WithCookieJar(jar),
	)
	if err != nil {
		t.Fatalf("NewHttpClient failed: %v", err)
	}

	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/app", nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}

	resp, err := doNavigationWithRedirects(ctx, client, req, nil)
	if err != nil {
		t.Fatalf("doNavigationWithRedirects failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
	if step != 4 {
		t.Errorf("Expected 4 steps in redirect chain, took %d", step)
	}
}
