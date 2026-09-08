package api

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestValidateWebProxyInput(t *testing.T) {
	valid, host, err := validateWebProxyInput(webProxyInput{HostID: "ssh-host", TargetURL: "https://192.168.1.1:8443/admin"})
	if err != nil || valid.String() != "https://192.168.1.1:8443/admin" || host != "192.168.1.1:8443" {
		t.Fatalf("valid target=%v host=%q err=%v", valid, host, err)
	}
	for name, value := range map[string]webProxyInput{
		"missing ssh host": {TargetURL: "http://192.168.1.1"},
		"unknown scheme":   {HostID: "ssh-host", TargetURL: "ftp://192.168.1.1"},
		"credentials":      {HostID: "ssh-host", TargetURL: "http://admin:secret@192.168.1.1"},
		"fragment":         {HostID: "ssh-host", TargetURL: "http://192.168.1.1/#settings"},
		"bad host header":  {HostID: "ssh-host", TargetURL: "http://192.168.1.1", UpstreamHost: "safe\r\nX-Bad: yes"},
	} {
		if _, _, err := validateWebProxyInput(value); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

func TestRewriteProxyURL(t *testing.T) {
	target, _ := url.Parse("http://192.168.1.1/admin")
	prefix := "/web-proxy/token"
	for input, expected := range map[string]string{
		"/assets/app.js?v=1":                     prefix + "/assets/app.js?v=1",
		"http://192.168.1.1/admin/login#form":    prefix + "/login#form",
		"//192.168.1.1/admin/image.png":          prefix + "/image.png",
		prefix + "/assets/already-proxied.js":    prefix + "/assets/already-proxied.js",
		"relative/page":                          "relative/page",
		"https://cdn.example.invalid/library.js": "https://cdn.example.invalid/library.js",
		"data:image/png;base64,AAAA":             "data:image/png;base64,AAAA",
	} {
		if actual := rewriteProxyURL(input, prefix, target); actual != expected {
			t.Errorf("rewriteProxyURL(%q)=%q, want %q", input, actual, expected)
		}
	}
}

func TestRewriteProxyURLAcceptsLoopbackTargetAlias(t *testing.T) {
	target, _ := url.Parse("http://192.168.0.111:9117")
	prefix := "/web-service-proxy/service-id"
	for _, input := range []string{
		"http://127.0.0.1:9117/UI/Login?ReturnUrl=%2FUI%2FDashboard",
		"//localhost:9117/UI/Login",
		"http://192.168.0.111/UI/Login",
	} {
		if actual := rewriteProxyURL(input, prefix, target); !strings.HasPrefix(actual, prefix+"/UI/Login") {
			t.Errorf("rewriteProxyURL(%q)=%q, want proxied login URL", input, actual)
		}
	}
	if actual := rewriteProxyURL("http://127.0.0.1:80/UI/Login", prefix, target); actual != "http://127.0.0.1:80/UI/Login" {
		t.Fatalf("rewriteProxyURL rewrote a different port: %q", actual)
	}
}

func TestModifyResponseRewritesTargetRedirect(t *testing.T) {
	target, _ := url.Parse("http://192.168.0.111:9117")
	session := &webProxySession{routePrefix: "/web-service-proxy/service-id", target: target}
	request, _ := http.NewRequest(http.MethodGet, "https://velin.example/web-service-proxy/service-id/UI/Dashboard", nil)
	request.Host = "velin.example"
	request.Header.Set("X-Forwarded-Host", "velin.example")
	response := &http.Response{
		Header:  http.Header{"Location": {"http://192.168.0.111/UI/Login?ReturnUrl=%2FUI%2FDashboard"}},
		Body:    io.NopCloser(strings.NewReader("")),
		Request: request,
	}
	if err := session.modifyResponse(response); err != nil {
		t.Fatal(err)
	}
	if actual := response.Header.Get("Location"); actual != "/web-service-proxy/service-id/UI/Login?ReturnUrl=%2FUI%2FDashboard" {
		t.Fatalf("rewritten redirect=%q", actual)
	}
	response.Header.Set("Location", "http://velin.example/UI/Login?ReturnUrl=%2FUI%2FDashboard")
	if err := session.modifyResponse(response); err != nil {
		t.Fatal(err)
	}
	if actual := response.Header.Get("Location"); actual != "/web-service-proxy/service-id/UI/Login?ReturnUrl=%2FUI%2FDashboard" {
		t.Fatalf("rewritten public-host redirect=%q", actual)
	}
}

func TestRewriteHTML(t *testing.T) {
	target, _ := url.Parse("http://router.internal")
	body := rewriteHTML([]byte(`<html><head><script src="/dashboard.js"></script></head><body><a href="/login">Login</a><img src="/logo.png" srcset="/small.png 1x, /large.png 2x"><form action="/save"></form><script>if (1 < 2) console.log("ok")</script></body></html>`), "/web-proxy/token", target)
	text := string(body)
	for _, expected := range []string{
		`data-velin-web-proxy="runtime"`,
		`<base href="/web-proxy/token/" data-velin-web-proxy="base"/>`,
		`const prefix="/web-proxy/token"`,
		`window.fetch=function(input,init)`,
		`XMLHttpRequest.prototype.open=function(method,url)`,
		`href="/web-proxy/token/login"`,
		`src="/web-proxy/token/logo.png"`,
		`srcset="/web-proxy/token/small.png 1x, /web-proxy/token/large.png 2x"`,
		`action="/web-proxy/token/save"`,
		`if (1 < 2) console.log("ok")`,
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("rewritten HTML missing %q: %s", expected, text)
		}
	}
	if bootstrap, dashboard := strings.Index(text, `data-velin-web-proxy="runtime"`), strings.Index(text, `src="/web-proxy/token/dashboard.js"`); bootstrap < 0 || dashboard < 0 || bootstrap > dashboard {
		t.Fatalf("runtime bootstrap must precede target scripts: %s", text)
	}
}

func TestRewriteHTMLUsesDocumentDirectoryForRelativeURLs(t *testing.T) {
	target, _ := url.Parse("http://router.internal")
	body := string(rewriteHTMLAtPath([]byte(`<html><head><script src="../libs/jquery.min.js"></script></head><body><img src="../jacket_medium.png"></body></html>`), "/web-service-proxy/service-id", target, "/UI/Login"))
	if !strings.Contains(body, `<base href="/web-service-proxy/service-id/UI/" data-velin-web-proxy="base"/>`) {
		t.Fatalf("relative URL base path missing: %s", body)
	}
	if !strings.Contains(body, `src="../libs/jquery.min.js"`) || !strings.Contains(body, `src="../jacket_medium.png"`) {
		t.Fatalf("relative URLs should remain relative to the document directory: %s", body)
	}
}

func TestRewriteHTMLRemovesOnlyUpstreamCSPMeta(t *testing.T) {
	target, _ := url.Parse("http://router.internal")
	prefix := "/web-service-proxy/service-id"
	original := `<html><head><meta http-equiv="Content-Security-Policy" content="script-src 'self' http: https: 'unsafe-inline' 'unsafe-eval'"><meta HTTP-EQUIV="content-security-policy-report-only" content="default-src 'none'"><meta name="viewport" content="width=device-width"><script type="module">import'data:text/javascript,if(!import.meta.resolve)throw Error("import.meta.resolve not supported")'</script><script type="module" src="/static/js/login.js"></script></head><body></body></html>`
	body := string(rewriteHTMLAtPath([]byte(original), prefix, target, "/login"))

	if strings.Contains(strings.ToLower(body), "content-security-policy") {
		t.Fatalf("upstream CSP meta was not removed: %s", body)
	}
	for _, expected := range []string{
		`<meta name="viewport" content="width=device-width"/>`,
		`data-velin-web-proxy="runtime"`,
		`<base href="/web-service-proxy/service-id/" data-velin-web-proxy="base"/>`,
		`import'data:text/javascript,if(!import.meta.resolve)throw Error("import.meta.resolve not supported")'`,
		`src="/web-service-proxy/service-id/static/js/login.js"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("rewritten HTML missing %q: %s", expected, body)
		}
	}
}

func TestRewriteHTMLRewritesInlineModuleImportsOnly(t *testing.T) {
	target, _ := url.Parse("http://router.internal")
	prefix := "/web-service-proxy/service-id"
	original := `<html><head><script type="module">import RefreshRuntime from "/@react-refresh"; import "/src/main.tsx";</script><script type="module" crossorigin="anonymous" src="/@vite/client"></script><script>const root = "/must-stay";</script></head></html>`
	body := string(rewriteHTML([]byte(original), prefix, target))

	if !strings.Contains(body, `from "/web-service-proxy/service-id/@react-refresh"`) ||
		!strings.Contains(body, `import "/web-service-proxy/service-id/src/main.tsx"`) {
		t.Fatalf("inline module imports were not rewritten: %s", body)
	}
	if !strings.Contains(body, `const root = "/must-stay";`) {
		t.Fatalf("ordinary inline script was modified: %s", body)
	}
	if strings.Count(body, `crossorigin="use-credentials"`) != 2 ||
		!strings.Contains(body, `src="/web-service-proxy/service-id/@vite/client"`) {
		t.Fatalf("module scripts do not carry proxy authentication: %s", body)
	}
}

func TestRewriteStableWebServiceNavigation(t *testing.T) {
	target, _ := url.Parse("http://router.internal")
	prefix := "/web-service-proxy/service-id"
	body := string(rewriteHTML([]byte(`<html><head><link rel="stylesheet" href="/web-service-proxy/assets/app.css"><script src="/web-service-proxy/assets/app.js"></script></head><body><a href="/admin">Admin</a><img src="/logo.png"></body></html>`), prefix, target))
	if !strings.Contains(body, `href="/admin?__velin_web_service=service-id"`) {
		t.Fatalf("navigation link did not use browser route: %s", body)
	}
	if !strings.Contains(body, `src="/web-service-proxy/service-id/logo.png"`) {
		t.Fatalf("resource URL did not use proxy path: %s", body)
	}
	for _, expected := range []string{
		`href="/web-service-proxy/service-id/web-service-proxy/assets/app.css"`,
		`src="/web-service-proxy/service-id/web-service-proxy/assets/app.js"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("reserved proxy-like resource path missing %q: %s", expected, body)
		}
	}
	if actual := rewriteBrowserRouteURL("/admin?tab=users", prefix, target); actual != "/admin?__velin_web_service=service-id&tab=users" {
		t.Fatalf("browser route=%q", actual)
	}
}

func TestModifyResponseRewritesGenericHTMLByExtension(t *testing.T) {
	target, _ := url.Parse("http://router.internal")
	request, _ := http.NewRequest(http.MethodGet, "http://router.internal/api/plugin-pages/jackett.html", nil)
	original := `<html><head><link rel="stylesheet" href="/web-service-proxy/assets/plugin.css"><script src="/web-service-proxy/assets/plugin.js"></script></head></html>`
	session := &webProxySession{routePrefix: "/web-service-proxy/service-id", target: target}
	response := &http.Response{
		Header:        http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
		Body:          io.NopCloser(strings.NewReader(original)),
		ContentLength: int64(len(original)),
		Request:       request,
	}
	if err := session.modifyResponse(response); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`href="/web-service-proxy/service-id/web-service-proxy/assets/plugin.css"`,
		`src="/web-service-proxy/service-id/web-service-proxy/assets/plugin.js"`,
	} {
		if !strings.Contains(string(body), expected) {
			t.Fatalf("generic HTML response missing %q: %s", expected, body)
		}
	}
	if actual := response.Header.Get("Content-Type"); actual != "text/html; charset=utf-8" {
		t.Fatalf("content type=%q", actual)
	}
}

func TestWebProxyBootstrapIncludesTargetMapping(t *testing.T) {
	target, _ := url.Parse("http://192.168.1.1/admin")
	script := webProxyBootstrap("/web-proxy/token", target)
	for _, expected := range []string{
		`const targetHost="192.168.1.1"`,
		`const targetPort="80"`,
		`const targetPath="/admin"`,
		`const serviceID=""`,
		`function browserRoute(value)`,
		`history.pushState=function`,
		`navigationAttributes`,
		`parsed.pathname=prefix+`,
		`window.WebSocket=new Proxy`,
		`navigator.sendBeacon=function`,
		`Element.prototype.setAttribute=function`,
		`window.open=function(url)`,
		`function targetHostMatches(value)`,
	} {
		if !strings.Contains(script, expected) {
			t.Errorf("bootstrap missing %q", expected)
		}
	}
}

func TestRewriteCSS(t *testing.T) {
	target, _ := url.Parse("http://router.internal")
	body := rewriteCSS([]byte(`body{background:url('/wall.png')} @import "/theme.css"; .icon{background:url(data:image/png;base64,AAAA)}`), "/web-proxy/token", target)
	text := string(body)
	for _, expected := range []string{
		`url('/web-proxy/token/wall.png')`,
		`@import "/web-proxy/token/theme.css"`,
		`url(data:image/png;base64,AAAA)`,
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("rewritten CSS missing %q: %s", expected, text)
		}
	}
}

func TestModifyResponseDoesNotRewriteJavaScript(t *testing.T) {
	target, _ := url.Parse("http://router.internal")
	request, _ := http.NewRequest(http.MethodGet, "https://velin.example/web-service-proxy/id/app.js", nil)
	original := `const base="/api/v1";const endpoint=base+"/themes";const route="/admin/*";`
	session := &webProxySession{routePrefix: "/web-service-proxy/id", target: target}
	response := &http.Response{
		Header: http.Header{
			"Content-Type":  {"application/javascript; charset=utf-8"},
			"ETag":          {`"upstream-version"`},
			"Last-Modified": {"Thu, 13 Aug 2026 00:00:00 GMT"},
		},
		Body:          io.NopCloser(strings.NewReader(original)),
		ContentLength: int64(len(original)),
		Request:       request,
	}
	if err := session.modifyResponse(response); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != original {
		t.Fatalf("JavaScript response was modified: %q", body)
	}
	if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("ETag") != "" || response.Header.Get("Last-Modified") != "" {
		t.Fatalf("unsafe proxy cache headers: %v", response.Header)
	}
}

func TestModifyResponseAllowsOnlySandboxOrigin(t *testing.T) {
	target, _ := url.Parse("http://router.internal")
	session := &webProxySession{routePrefix: "/web-service-proxy/id", target: target}
	request, _ := http.NewRequest(http.MethodGet, "https://velin.example/web-service-proxy/id/src/main.tsx", nil)
	request.Header.Set("Origin", "null")
	response := &http.Response{Header: make(http.Header), Request: request}
	if err := session.modifyResponse(response); err != nil {
		t.Fatal(err)
	}
	if response.Header.Get("Access-Control-Allow-Origin") != "null" || response.Header.Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("sandbox CORS headers=%v", response.Header)
	}
	if !strings.Contains(strings.Join(response.Header.Values("Vary"), ","), "Origin") {
		t.Fatalf("sandbox CORS response does not vary by origin: %v", response.Header)
	}

	request.Header.Set("Origin", "https://attacker.example")
	response = &http.Response{Header: make(http.Header), Request: request}
	if err := session.modifyResponse(response); err != nil {
		t.Fatal(err)
	}
	if response.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("ordinary cross-origin request was allowed: %v", response.Header)
	}
}

func TestRewriteViteDevelopmentJavaScript(t *testing.T) {
	target, _ := url.Parse("http://cleaner.internal")
	prefix := "/web-service-proxy/cleaner"
	source := `import'data:text/javascript,if(!import.meta.resolve)throw Error("import.meta.resolve not supported")'; import RefreshRuntime from "/@react-refresh"; import App from "/src/App.tsx"; import("/src/lazy.tsx"); const api = "/api/status";`
	actual := string(rewriteViteJavaScript([]byte(source), prefix, target))
	for _, expected := range []string{
		`import'data:text/javascript,if(!import.meta.resolve)throw Error("import.meta.resolve not supported")'`,
		`from "/web-service-proxy/cleaner/@react-refresh"`,
		`from "/web-service-proxy/cleaner/src/App.tsx"`,
		`import("/web-service-proxy/cleaner/src/lazy.tsx")`,
		`const api = "/api/status"`,
	} {
		if !strings.Contains(actual, expected) {
			t.Errorf("rewritten Vite JavaScript missing %q: %s", expected, actual)
		}
	}
}

func TestViteModeOnlyRewritesJavaScriptAfterDevelopmentHTML(t *testing.T) {
	target, _ := url.Parse("http://cleaner.internal")
	session := &webProxySession{routePrefix: "/web-service-proxy/cleaner", target: target, stable: true}
	htmlRequest, _ := http.NewRequest(http.MethodGet, "http://velin.example/web-service-proxy/cleaner/", nil)
	responseHeader := make(http.Header)
	policy := webProxyResponsePolicy{header: responseHeader, host: "velin.example"}
	htmlRequest = htmlRequest.WithContext(context.WithValue(htmlRequest.Context(), webProxyResponsePolicyKey{}, policy))
	htmlResponse := &http.Response{
		Header:  http.Header{"Content-Type": {"text/html; charset=utf-8"}},
		Body:    io.NopCloser(strings.NewReader(`<html><head><script type="module" src="/@vite/client"></script></head></html>`)),
		Request: htmlRequest,
	}
	if err := session.modifyResponse(htmlResponse); err != nil {
		t.Fatal(err)
	}
	if csp := responseHeader.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox allow-scripts allow-forms allow-popups allow-modals allow-downloads allow-same-origin") {
		t.Fatalf("Vite response does not retain the NAS authorization origin: %q", csp)
	}
	jsRequest, _ := http.NewRequest(http.MethodGet, "http://velin.example/web-service-proxy/cleaner/src/main.tsx", nil)
	jsResponse := &http.Response{
		Header:  http.Header{"Content-Type": {"application/javascript; charset=utf-8"}},
		Body:    io.NopCloser(strings.NewReader(`import App from "/src/App.tsx";`)),
		Request: jsRequest,
	}
	if err := session.modifyResponse(jsResponse); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(jsResponse.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `from "/web-service-proxy/cleaner/src/App.tsx"`) {
		t.Fatalf("Vite module import was not rewritten: %s", body)
	}
}

func TestStableViteFirstResponseAllowsNASAuthorizationCookie(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<html><head><script type="module" src="/@vite/client"></script></head></html>`)
	}))
	defer upstream.Close()

	target, _ := url.Parse(upstream.URL)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	session := &webProxySession{
		target: target, upstream: target.Host, transport: transport,
		routePrefix: "/web-service-proxy/cleaner", stable: true,
	}
	session.proxy = session.reverseProxy()
	request := httptest.NewRequest(http.MethodGet, "https://velin.example/web-service-proxy/cleaner/", nil)
	recorder := httptest.NewRecorder()

	serveWebProxySession(recorder, request, session)

	policies := recorder.Result().Header.Values("Content-Security-Policy")
	if len(policies) != 1 || !strings.Contains(policies[0], "allow-same-origin") {
		t.Fatalf("first Vite response CSP=%q", policies)
	}
	if !strings.Contains(recorder.Body.String(), `src="/web-service-proxy/cleaner/@vite/client"`) {
		t.Fatalf("first Vite response body=%s", recorder.Body.String())
	}
}

func TestPathProxyDisablesConditionalUpstreamCache(t *testing.T) {
	target, _ := url.Parse("http://router.internal")
	session := &webProxySession{routePrefix: "/web-service-proxy/id", target: target, upstream: "router.internal"}
	request, _ := http.NewRequest(http.MethodGet, "https://velin.example/web-service-proxy/id/app.js", nil)
	request.Header.Set("If-None-Match", `"cached"`)
	request.Header.Set("If-Modified-Since", "Thu, 13 Aug 2026 00:00:00 GMT")
	session.reverseProxy().Director(request)
	if request.Header.Get("If-None-Match") != "" || request.Header.Get("If-Modified-Since") != "" {
		t.Fatalf("conditional cache headers reached upstream: %v", request.Header)
	}
}

func TestProxyCookieIsolation(t *testing.T) {
	request := &http.Request{Header: http.Header{"Cookie": {"velin_session=secret; csrf=upstream"}}}
	if actual := upstreamCookies(request); actual != "csrf=upstream" {
		t.Fatalf("upstream cookies=%q", actual)
	}
	policy := webProxyCSP("velin.example", "/web-proxy/token", false)
	if strings.Contains(policy, "allow-same-origin") {
		t.Fatal("ordinary proxy pages share the Velin origin")
	}
	if strings.Contains(policy, "connect-src 'self'") {
		t.Fatal("proxy CSP allows root-origin API connections")
	}
	if !strings.Contains(policy, "script-src http://velin.example/web-proxy/token/ https://velin.example/web-proxy/token/ data: 'unsafe-inline'") {
		t.Fatalf("proxy CSP does not allow Vite legacy detection modules: %q", policy)
	}
}

func TestProxyCookieJarRestoresCookiesForRootNavigation(t *testing.T) {
	target, _ := url.Parse("http://music.internal")
	session := &webProxySession{routePrefix: "/web-service-proxy/service-id", target: target, cookies: make(map[string]*http.Cookie)}
	session.captureUpstreamCookies([]string{"VELIN_SID=session-value; Path=/; HttpOnly"})
	request, _ := http.NewRequest(http.MethodGet, "https://velin.example/?__velin_web_service=service-id", nil)
	if actual := session.upstreamCookieHeader(request); actual != "VELIN_SID=session-value" {
		t.Fatalf("restored upstream cookie=%q", actual)
	}
	request.Header.Set("Cookie", "VELIN_SID=new-value")
	if actual := session.upstreamCookieHeader(request); actual != "VELIN_SID=new-value" {
		t.Fatalf("browser cookie should override jar=%q", actual)
	}
}

func TestRewriteRequestOrigin(t *testing.T) {
	target, _ := url.Parse("http://router.internal/admin")
	request, _ := http.NewRequest(http.MethodGet, "http://velin.example/web-proxy/token/socket", nil)
	request.Header.Set("Origin", "https://velin.example")
	request.Header.Set("Referer", "https://velin.example/web-proxy/token/device.html?device=abc")

	rewriteRequestOrigin(request, "/web-proxy/token", target, "dashboard.internal:8080")

	if actual := request.Header.Get("Origin"); actual != "http://dashboard.internal:8080" {
		t.Fatalf("origin=%q", actual)
	}
	if actual := request.Header.Get("Referer"); actual != "http://dashboard.internal:8080/admin/device.html?device=abc" {
		t.Fatalf("referer=%q", actual)
	}
}

func TestRequestProto(t *testing.T) {
	request, _ := http.NewRequest(http.MethodGet, "http://velin.example/", nil)
	request.Header.Set("X-Forwarded-Proto", "https")
	if actual := requestProto(request); actual != "https" {
		t.Fatalf("proto=%q", actual)
	}
}

func TestJoinRawQuery(t *testing.T) {
	if actual := joinRawQuery("view=full", "page=2"); actual != "view=full&page=2" {
		t.Fatalf("query=%q", actual)
	}
}

func TestLogRequestPathRedactsProxyToken(t *testing.T) {
	if actual := logRequestPath("/web-proxy/secret-token/assets/app.js"); actual != "/web-proxy/[redacted]/assets/app.js" {
		t.Fatalf("redacted path=%q", actual)
	}
	if actual := logRequestPath("/api/hosts"); actual != "/api/hosts" {
		t.Fatalf("regular path=%q", actual)
	}
	if actual := logRequestPath("/web-service-proxy/service-id/assets/app.js"); actual != "/web-service-proxy/[redacted]/assets/app.js" {
		t.Fatalf("stable proxy path=%q", actual)
	}
}

func TestStableWebProxyPrefix(t *testing.T) {
	session := &webProxySession{token: "temporary", routePrefix: stableWebProxyPrefix("service-id")}
	if actual := session.prefix(); actual != "/web-service-proxy/service-id" {
		t.Fatalf("stable prefix=%q", actual)
	}
	session.routePrefix = ""
	if actual := session.prefix(); actual != "/web-proxy/temporary" {
		t.Fatalf("temporary prefix=%q", actual)
	}
	session.rootProxy = true
	if actual := session.prefix(); actual != "" {
		t.Fatalf("root proxy prefix=%q", actual)
	}
}

func TestServiceIDFromReferer(t *testing.T) {
	request, _ := http.NewRequest(http.MethodGet, "https://velin.example/UI/Login", nil)
	request.Host = "velin.example"
	request.Header.Set("Referer", "https://velin.example/web-service-proxy/service-id/UI/Dashboard")
	if actual := serviceIDFromReferer(request); actual != "service-id" {
		t.Fatalf("service id from proxy referer=%q", actual)
	}
	request.Header.Set("Referer", "https://velin.example/?__velin_web_service=service-id")
	if actual := serviceIDFromReferer(request); actual != "service-id" {
		t.Fatalf("service id from marked referer=%q", actual)
	}
	request.Header.Set("Referer", "https://other.example/web-service-proxy/service-id/UI/Dashboard")
	if actual := serviceIDFromReferer(request); actual != "" {
		t.Fatalf("cross-origin referer leaked service id=%q", actual)
	}
}

func TestRootProxyKeepsRequestAtRoot(t *testing.T) {
	target, _ := url.Parse("http://router.internal/admin")
	session := &webProxySession{rootProxy: true, target: target, upstream: "router.internal"}
	request, _ := http.NewRequest(http.MethodGet, "http://velin.example:18080/assets/app.js?x=1", nil)
	session.reverseProxy().Director(request)
	if request.URL.String() != "http://router.internal/admin/assets/app.js?x=1" {
		t.Fatalf("root proxy URL=%q", request.URL.String())
	}
	if request.Header.Get("X-Forwarded-Prefix") != "" {
		t.Fatalf("root proxy forwarded prefix=%q", request.Header.Get("X-Forwarded-Prefix"))
	}
}

func TestRootProxyCannotOverwriteVelinSession(t *testing.T) {
	target, _ := url.Parse("http://router.internal")
	session := &webProxySession{rootProxy: true, target: target}
	request, _ := http.NewRequest(http.MethodGet, "http://velin.example:18080/", nil)
	response := &http.Response{
		Header:  http.Header{"Set-Cookie": {cookieName + "=attacker; Path=/", "upstream=value; Path=/"}},
		Body:    io.NopCloser(bytes.NewReader(nil)),
		Request: request,
	}
	if err := session.modifyResponse(response); err != nil {
		t.Fatal(err)
	}
	cookies := response.Header.Values("Set-Cookie")
	if len(cookies) != 1 || strings.Contains(cookies[0], cookieName+"=") || !strings.Contains(cookies[0], "upstream=value") {
		t.Fatalf("root proxy cookies=%v", cookies)
	}
}

func TestPathProxyCannotOverwriteVelinSecurityCookies(t *testing.T) {
	target, _ := url.Parse("http://router.internal")
	session := &webProxySession{routePrefix: "/web-service-proxy/id", target: target}
	request, _ := http.NewRequest(http.MethodGet, "https://velin.example/web-service-proxy/id/", nil)
	response := &http.Response{
		Header: http.Header{"Set-Cookie": {cookieName + "=attacker; Path=/", csrfCookieName + "=attacker; Path=/", "upstream=value; Path=/"}},
		Body:   io.NopCloser(bytes.NewReader(nil)), Request: request,
	}
	if err := session.modifyResponse(response); err != nil {
		t.Fatal(err)
	}
	cookies := response.Header.Values("Set-Cookie")
	if len(cookies) != 1 || !strings.Contains(cookies[0], "upstream=value") {
		t.Fatalf("path proxy cookies=%v", cookies)
	}
}

func TestHostPortValidation(t *testing.T) {
	manager := &webProxyManager{listenAddress: "127.0.0.1", hostPorts: make(map[string]*hostPortWebProxy)}
	if err := manager.checkHostPort("service", 0); err == nil {
		t.Fatal("invalid host port was accepted")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	if err = manager.checkHostPort("service", port); err == nil {
		t.Fatal("occupied host port was accepted")
	}
	manager.hostPorts["other"] = &hostPortWebProxy{port: port + 1}
	if err = manager.checkHostPort("service", port+1); err == nil {
		t.Fatal("duplicate configured host port was accepted")
	}
}

func TestHostPortListenerLifecycle(t *testing.T) {
	probe, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()
	manager := &webProxyManager{listenAddress: "127.0.0.1", hostPorts: make(map[string]*hostPortWebProxy)}
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	if err = manager.setHostPort("service", port, handler); err != nil {
		t.Fatal(err)
	}
	response, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/health")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("host port response=%d %q", response.StatusCode, body)
	}
	manager.deleteHostPort("service")
	released, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("host port was not released: %v", err)
	}
	_ = released.Close()
}

func TestHostPortAccessIsSingleUseAndServiceScoped(t *testing.T) {
	manager := &webProxyManager{hostPortAccess: make(map[string]*hostPortAccess)}
	token, err := manager.issueHostPortAccess("user", "service", "auth-hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.hostPortAuthorization(token, "user", "other", true); ok {
		t.Fatal("access token was accepted for another service")
	}
	token, _ = manager.issueHostPortAccess("user", "service", "auth-hash")
	if hash, ok := manager.hostPortAuthorization(token, "user", "service", true); !ok || hash != "auth-hash" {
		t.Fatalf("activation hash=%q ok=%v", hash, ok)
	}
	if _, ok := manager.hostPortAuthorization(token, "user", "service", true); ok {
		t.Fatal("access token activated twice")
	}
	if hash, ok := manager.hostPortAuthorization(token, "user", "service", false); !ok || hash != "auth-hash" {
		t.Fatalf("activated access hash=%q ok=%v", hash, ok)
	}
}

func TestUpstreamCookiesRemoveVelinSecurityCookies(t *testing.T) {
	request := &http.Request{Header: http.Header{"Cookie": {"velin_session=secret; velin_csrf=token; velin_host_port_abcd=ticket; upstream=value"}}}
	if actual := upstreamCookies(request); actual != "upstream=value" {
		t.Fatalf("upstream cookies=%q", actual)
	}
}

func TestHostPortURL(t *testing.T) {
	request, _ := http.NewRequest(http.MethodGet, "https://[2001:db8::1]:8377/api/web-services/id/open", nil)
	if actual := hostPortWebProxyURL(request, 18080); actual != "http://[2001:db8::1]:18080/" {
		t.Fatalf("host port URL=%q", actual)
	}
	request.Host = "velin.example"
	if actual := hostPortWebProxyURL(request, 18080); actual != "http://velin.example:18080/" {
		t.Fatalf("domain host port URL=%q", actual)
	}
	if actual := configuredListenPort("0.0.0.0:8377"); actual != 8377 {
		t.Fatalf("configured listen port=%d", actual)
	}
}

func TestRootProxyCSPUsesRootPath(t *testing.T) {
	policy := webProxyCSP("velin.example:18080", "", false)
	if strings.Contains(policy, "velin.example:18080//") || !strings.Contains(policy, "ws://velin.example:18080/") ||
		!strings.Contains(policy, "base-uri http://velin.example:18080/ https://velin.example:18080/") {
		t.Fatalf("root proxy CSP=%q", policy)
	}
}
