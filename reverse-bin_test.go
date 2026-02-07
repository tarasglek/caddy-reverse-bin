package reversebin

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

func TestReverseBin_UnmarshalCaddyfile(t *testing.T) {
	content := `reverse-bin /some/file a b c d 1 {
  dir /somewhere
  env foo=bar what=ever
  pass_env some_env other_env
  pass_all_env
}`
	d := caddyfile.NewTestDispenser(content)
	var c ReverseBin
	if err := c.UnmarshalCaddyfile(d); err != nil {
		t.Fatalf("Cannot parse caddyfile: %v", err)
	}

	expected := ReverseBin{
		Executable:       []string{"/some/file", "a", "b", "c", "d", "1"},
		WorkingDirectory: "/somewhere",
		Envs:             []string{"foo=bar", "what=ever"},
		PassEnvs:         []string{"some_env", "other_env"},
		PassAll:          true,
	}

	if !reflect.DeepEqual(c, expected) {
		t.Fatal("Parsing yielded invalid result.")
	}
}

type NoOpNextHandler struct{}

func (n NoOpNextHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	// Do Nothing
	return nil
}

func _TestReverseBin_ServeHTTPPost(t *testing.T) {
	testSetup := []struct {
		name         string
		uri          string
		method       string
		requestBody  string
		responseBody string
		cgi          ReverseBin
		statusCode   int
		chunked      bool
	}{
		{
			name: "POST Request",
			cgi: ReverseBin{
				Executable: "test/example_post",
				ScriptName: "/foo.cgi",
				Args:       []string{"arg1", "arg2"},
				Envs:       []string{"CGI_GLOBAL=whatever"},
				logger:     zaptest.NewLogger(t, zaptest.Level(zap.ErrorLevel)),
			},
			uri:    "foo.cgi/some/path?x=y",
			method: http.MethodPost,
			requestBody: `Chunked HTTP Request Body
With some awesome stuff in there like
this and that and also
this and that and also
this and that and also
this and that and also
this and that and also`,
			statusCode: 200,
			responseBody: `PATH_INFO [/some/path]
CGI_GLOBAL [whatever]
Arg 1 [arg1]
QUERY_STRING [x=y]
REMOTE_USER []
HTTP_TOKEN_CLAIM_USER []
CGI_LOCAL is unset
Chunked HTTP Request Body
With some awesome stuff in there like
this and that and also
this and that and also
this and that and also
this and that and also
this and that and also`,
		},
		{
			name: "POST Request with chunked Transfer-Encoding In-Memory",
			cgi: ReverseBin{
				Executable:  "test/example_post",
				ScriptName:  "/foo.cgi",
				Args:        []string{"arg1", "arg2"},
				Envs:        []string{"CGI_GLOBAL=whatever"},
				logger:      zaptest.NewLogger(t, zaptest.Level(zap.ErrorLevel)),
				BufferLimit: 200,
			},
			uri:    "foo.cgi/some/path?x=y",
			method: http.MethodPost,
			requestBody: `Chunked HTTP Request Body
With some awesome stuff in there like
this and that and also
this and that and also
this and that and also
this and that and also
this and that and also`,
			statusCode: 200,
			responseBody: `PATH_INFO [/some/path]
CGI_GLOBAL [whatever]
Arg 1 [arg1]
QUERY_STRING [x=y]
REMOTE_USER []
HTTP_TOKEN_CLAIM_USER []
CGI_LOCAL is unset
Chunked HTTP Request Body
With some awesome stuff in there like
this and that and also
this and that and also
this and that and also
this and that and also
this and that and also`,
			chunked: true,
		},
		{
			name: "POST Request with chunked Transfer-Encoding tempfile",
			cgi: ReverseBin{
				Executable:  "test/example_post",
				ScriptName:  "/foo.cgi",
				Args:        []string{"arg1", "arg2"},
				Envs:        []string{"CGI_GLOBAL=whatever"},
				logger:      zaptest.NewLogger(t, zaptest.Level(zap.ErrorLevel)),
				BufferLimit: 100,
			},
			uri:    "foo.cgi/some/path?x=y",
			method: http.MethodPost,
			requestBody: `Chunked HTTP Request Body
With some awesome stuff in there like
this and that and also
this and that and also
this and that and also
this and that and also
this and that and also`,
			statusCode: 200,
			responseBody: `PATH_INFO [/some/path]
CGI_GLOBAL [whatever]
Arg 1 [arg1]
QUERY_STRING [x=y]
REMOTE_USER []
HTTP_TOKEN_CLAIM_USER []
CGI_LOCAL is unset
Chunked HTTP Request Body
With some awesome stuff in there like
this and that and also
this and that and also
this and that and also
this and that and also
this and that and also`,
			chunked: true,
		},
	}

	for _, testCase := range testSetup {
		t.Run(testCase.name, func(t *testing.T) {
			res := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/foo.cgi/some/path?x=y", nil)

			if testCase.chunked {
				req.Header.Set("Transfer-Encoding", "chunked")
				req.TransferEncoding = []string{"chunked"}
			} else {
				cl := len(testCase.requestBody)
				req.Header.Set("Content-Length", strconv.Itoa(cl))
				req.ContentLength = int64(cl)
			}
			req.Body = io.NopCloser(strings.NewReader(testCase.requestBody))

			repl := caddy.NewReplacer()
			req = req.WithContext(context.WithValue(req.Context(), caddy.ReplacerCtxKey, repl))

			if err := testCase.cgi.ServeHTTP(res, req, NoOpNextHandler{}); err != nil {
				t.Fatalf("Cannot serve http: %v", err)
			}

			if res.Code != testCase.statusCode {
				t.Errorf("Unexpected statusCode %d. Expected %d.", res.Code, testCase.statusCode)
			}

			bodyString := strings.TrimSpace(res.Body.String())
			if bodyString != testCase.responseBody {
				t.Errorf("Unexpected body\n========== Got ==========\n%s\n========== Wanted ==========\n%s", bodyString, testCase.responseBody)
			}
		})
	}
}
