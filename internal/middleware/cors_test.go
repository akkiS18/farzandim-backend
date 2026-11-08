package middleware

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestParseAllowedDomains(t *testing.T) {
	got := ParseAllowedDomains(" akademx.uz, FARZANDIM-EDU.uz ,,*.example.uz, .foo.uz")
	want := []string{"akademx.uz", "farzandim-edu.uz", "example.uz", "foo.uz"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseAllowedDomains = %v, want %v", got, want)
	}
	if len(ParseAllowedDomains("")) != 0 {
		t.Fatal("empty value must yield no domains")
	}
}

func TestOriginAllowed(t *testing.T) {
	domains := ParseAllowedDomains("akademx.uz,farzandim-edu.uz")

	allow := []string{
		"https://test-school.farzandim-edu.uz",
		"https://api.akademx.uz",
		"https://akademx.uz",
		"https://farzandim-edu.uz",
		"https://ADMIN.Farzandim-Edu.uz",
		"https://test-school.farzandim-edu.uz:443",
	}
	for _, o := range allow {
		if !originAllowed(o, domains) {
			t.Errorf("originAllowed(%q) = false, want true", o)
		}
	}

	reject := []string{
		"https://evilakademx.uz",
		"https://evilfarzandim-edu.uz",
		"https://akademx.uz.evil.com",
		"https://evil.com/?x=akademx.uz",
		"https://akademx.uz@evil.com",
		"http://test-school.farzandim-edu.uz",
		"",
		"garbage",
		"null",
		"//akademx.uz",
	}
	for _, o := range reject {
		if originAllowed(o, domains) {
			t.Errorf("originAllowed(%q) = true, want false", o)
		}
	}

	if originAllowed("https://akademx.uz", nil) {
		t.Error("no configured domains must allow nothing")
	}
}

func TestCORSMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORS(ParseAllowedDomains("akademx.uz,farzandim-edu.uz")))
	r.GET("/ping", func(c *gin.Context) { c.String(200, "ok") })

	do := func(method, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/ping", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	w := do(http.MethodOptions, "https://test-school.farzandim-edu.uz")
	if w.Code != 204 {
		t.Fatalf("preflight status = %d, want 204", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://test-school.farzandim-edu.uz" {
		t.Errorf("Allow-Origin = %q", got)
	}
	if w.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Error("Allow-Credentials must be true for an allowed origin")
	}
	if w.Header().Get("Vary") != "Origin" {
		t.Errorf("Vary = %q, want Origin", w.Header().Get("Vary"))
	}

	w = do(http.MethodGet, "https://evilakademx.uz")
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("evil origin got Allow-Origin %q", got)
	}
	if w.Header().Get("Vary") != "Origin" {
		t.Error("Vary: Origin must be set even for rejected origins")
	}

	w = do(http.MethodGet, "http://localhost:6501")
	if w.Header().Get("Access-Control-Allow-Origin") != "http://localhost:6501" {
		t.Error("localhost dev origin must stay allowed")
	}
}
