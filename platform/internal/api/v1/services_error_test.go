package v1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/api/middleware"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

func TestAPIErrorEnvelopeMasksServerFailureDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, failure := range []error{
		fmt.Errorf("private SQL and connection credential"),
		pkgerrors.Wrap(pkgerrors.ErrInternal, "private SQL and connection credential"),
	} {
		r := gin.New()
		r.Use(middleware.RequestID())
		r.GET("/failure", func(c *gin.Context) { respondError(c, failure) })
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/failure", nil))
		var envelope map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusInternalServerError || envelope["code"] != "INTERNAL" || envelope["request_id"] == "" || strings.Contains(w.Body.String(), "private SQL") {
			t.Fatalf("server error must be masked with request ID: %d %s", w.Code, w.Body.String())
		}
	}
}
