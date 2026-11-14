//go:build darwin

package boundary

import (
	"encoding/binary"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"
)

func testGroupCompatibilityUUID(gid uint32) [aclPrincipalSize]byte {
	// This is Libinfo membership.c's synthesized group prefix. The prose in
	// the installed membership.h is stale and names a different value.
	result := [aclPrincipalSize]byte{
		0xab, 0xcd, 0xef, 0xab,
		0xcd, 0xef,
		0xab, 0xcd,
		0xef, 0xab,
		0xcd, 0xef,
	}
	binary.BigEndian.PutUint32(result[12:], gid)
	return result
}

func TestDarwinACLPrincipalResolverRequiresOwnerUserIdentity(t *testing.T) {
	uid := uint32(os.Getuid())

	isOwner, err := darwinACLPrincipalIsOwner(ownerCompatibilityUUID(uid), uid)
	if err != nil || !isOwner {
		t.Fatalf("operator user UUID: owner = %v, error = %v", isOwner, err)
	}

	isOwner, err = darwinACLPrincipalIsOwner(ownerCompatibilityUUID(uid^1), uid)
	if err != nil {
		t.Fatalf("different user UUID = %v", err)
	}
	if isOwner {
		t.Fatal("different user UUID classified as owner")
	}

	// Match the numeric id deliberately: the returned identity type must
	// still distinguish a group qualifier from the owner user.
	isOwner, err = darwinACLPrincipalIsOwner(testGroupCompatibilityUUID(uid), uid)
	if err != nil {
		t.Fatalf("same-id group UUID = %v", err)
	}
	if isOwner {
		t.Fatal("group UUID with the owner's numeric id classified as owner")
	}
}

func TestDarwinACLInspectorWiresMembershipResolver(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "acl_darwin.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	wired := false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 3 {
			return true
		}
		callee, ok := call.Fun.(*ast.Ident)
		if !ok || callee.Name != "parseACLAttributeBuffer" {
			return true
		}
		resolver, ok := call.Args[2].(*ast.Ident)
		wired = ok && resolver.Name == "darwinACLPrincipalIsOwner"
		return !wired
	})
	if !wired {
		t.Fatal("Darwin ACL snapshot parser is not wired to the native membership resolver")
	}
}
