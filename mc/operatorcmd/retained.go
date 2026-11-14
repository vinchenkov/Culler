package operatorcmd

import "slices"

// Retained host commands use the same request checks as the public surface.
// New agents discover Board writes and Project setup through the public catalog.
func retainedDefinition(args []string) (Definition, bool) {
	if len(args) < 2 {
		return Definition{}, false
	}
	var d Definition
	switch {
	case slices.Equal(args[:2], []string{"task", "add"}):
		priority := integer("priority", "The task priority.", false, -1)
		maximum := int64(3)
		priority.Maximum = &maximum
		d = Definition{Path: []string{"task", "add"}, Mutates: true, Permission: "task.add",
			Arguments: []Field{textField("title", "The task title.", true)},
			Flags:     []Field{textField("project", "The Project identifier.", true), textField("description", "The task description.", false), priority}}
	case slices.Equal(args[:2], []string{"project", "add"}):
		d = Definition{Path: []string{"project", "add"}, Mutates: true, Permission: "project.add",
			Arguments: []Field{textField("project", "The Project identifier.", true)},
			Flags: []Field{textField("title", "The Project title.", true), boolean("paused", "Create the Project paused."),
				textField("jurisdiction", "The Project jurisdiction.", false), textField("sandbox-profile", "The existing sandbox profile.", true)}}
	default:
		return Definition{}, false
	}
	return d, true
}
