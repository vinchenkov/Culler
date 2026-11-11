package main

import (
	"database/sql"
	"io"
	"os"

	"mc/dispatch"
	"mc/verbs"
)

func cmdProjectRole(args []string, stdin io.Reader) (any, error) {
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	fs := newFlags("mc project " + args[0])
	project := fs.String("project", "", "the Project identifier")
	role := fs.String("role", "", "the pipeline role")
	request := fs.String("request", "-", "the request as JSON; - reads standard input")
	if err := parse(fs, args[1:]); err != nil {
		return nil, err
	}
	if fs.NArg() != 0 {
		return nil, verbs.Usagef("The customization command has unexpected arguments.")
	}
	switch args[0] {
	case "role-show":
		return withSpineRead(func(db *sql.DB) (any, error) { return verbs.ProjectRoleShow(db, id, *project, dispatch.Role(*role)) })
	case "role-save":
		var a verbs.RoleSaveRequest
		if err := readDeliveryInput(*request, stdin, &a); err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.ProjectRoleSave(db, id, a) })
	case "role-unblock":
		var a verbs.RoleUnblockRequest
		if err := readDeliveryInput(*request, stdin, &a); err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.ProjectRoleUnblock(db, id, a) })
	}
	return nil, verbs.Usagef("Unknown customization action.")
}

func cmdRunCustomization(args []string) (any, error) {
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	if args[0] == "customization" {
		if len(args) != 2 {
			return nil, verbs.Usagef("The customization read requires a run identifier.")
		}
		return withSpineRead(func(db *sql.DB) (any, error) { return verbs.RunCustomization(db, id, args[1]) })
	}
	fs := newFlags("mc run customization-conflict")
	request := fs.String("request", "-", "the conflict as JSON; - reads standard input")
	if err := parse(fs, args[1:]); err != nil {
		return nil, err
	}
	if fs.NArg() != 0 {
		return nil, verbs.Usagef("The conflict command has unexpected arguments.")
	}
	var a verbs.CustomizationConflictRequest
	if err := readDeliveryInput(*request, os.Stdin, &a); err != nil {
		return nil, err
	}
	return withSpine(func(db *sql.DB) (any, error) { return verbs.RunCustomizationConflict(db, id, a) })
}
