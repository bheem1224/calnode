package handler

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/access"
	"github.com/calnode/calnode/internal/db"
)

func TestAuthenticatedRoutesHaveExplicitPermission(t *testing.T) {
	source, err := os.ReadFile("../server/server.go")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := parser.ParseFile(token.NewFileSet(), "server.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	ast.Inspect(tree, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "RequireAuth" {
			return true
		}
		for _, arg := range call.Args {
			if method, ok := arg.(*ast.SelectorExpr); ok {
				count++
				if _, known := handlerPermissions[method.Sel.Name]; !known {
					t.Errorf("unclassified authenticated handler: %s", method.Sel.Name)
				}
			}
		}
		return true
	})
	if count < 50 {
		t.Fatalf("route inventory unexpectedly small: %d", count)
	}
	for name, id := range handlerPermissions {
		if !access.Full().Has(id) {
			t.Errorf("%s uses unknown permission %s", name, id)
		}
	}
}

func TestMCPGrantsRejectWritesAndMissingIdentity(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	h := New(database, slog.Default())
	ctx := withMCPCaller(context.Background(), mcpCaller{UserID: "reader", Permissions: access.Grants{"bookings.read": "own", "mcp.connect": "own"}})
	if _, err := h.authorizeMCPTool(ctx, "bookings.cancel", "booking", "unowned"); err == nil {
		t.Fatal("read-only MCP grant allowed cancellation")
	}
	if _, err := h.authorizeMCPTool(ctx, "bookings.read", "booking", "unowned"); err == nil {
		t.Fatal("own scope allowed unknown booking")
	}
	request := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.MCPCallerMiddleware(nil).ServeHTTP(rec, request)
	if rec.Code != 401 {
		t.Fatalf("missing bearer identity status=%d", rec.Code)
	}
}

func TestMCPSnapshotCannotGrowAfterPromotionAndRevokesImmediately(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO roles(id,name,permissions_json) VALUES('assistant','Assistant','{"mcp.connect":"own","bookings.read":"own"}')`,
		`INSERT INTO users(id,email,name,custom_role_id) VALUES('assistant','assistant@example.com','Assistant','assistant')`,
	} {
		if _, err = database.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	h := New(database, slog.Default())
	h.baseURL = "https://calendar.example.com"
	token := "mcat_test_snapshot"
	frozen := access.Grants{"mcp.connect": "own", "bookings.read": "own"}.JSON()
	if _, err = database.Exec(`INSERT INTO oauth_access_tokens(id,user_id,client_id,token_hash,resource,expires_at,permissions_json,created_at) VALUES('token','assistant','app',?,'https://calendar.example.com/mcp','2099-01-01T00:00:00Z',?,'2026-11-18T00:00:00Z')`, hashAPIKey(token), frozen); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Exec(`UPDATE users SET is_admin=1 WHERE id='assistant'`); err != nil {
		t.Fatal(err)
	}
	principal, _, err := h.mcpPrincipal(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Permissions["bookings.read"] != "own" || principal.Permissions.Has("bookings.cancel") {
		t.Fatalf("promotion expanded MCP grant: %v", principal.Permissions)
	}
	if _, err = database.Exec(`UPDATE users SET is_admin=0 WHERE id='assistant'`); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Exec(`UPDATE roles SET permissions_json='{}' WHERE id='assistant'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err = h.mcpPrincipal(context.Background(), token); err == nil {
		t.Fatal("MCP connection survived removal of connect permission")
	}
}
