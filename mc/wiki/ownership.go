package wiki

import (
	"context"
	"slices"

	"mc/repository"
)

// ValidateTaskCommit permits only the assigned wiki or the accepted wiki from a merged destination.
func ValidateTaskCommit(ctx context.Context, g repository.Git, input, destination, result string) error {
	baseline := input
	merged, err := g.IsAncestor(ctx, destination, result)
	if err != nil {
		return err
	}
	if merged {
		baseline = destination
	}
	beforeTree, err := g.TreeID(ctx, baseline, "openwiki")
	if err != nil {
		return err
	}
	afterTree, err := g.TreeID(ctx, result, "openwiki")
	if err != nil {
		return err
	}
	unchanged := beforeTree == afterTree
	if unchanged && beforeTree == "" {
		// A file or symbolic link at the wiki root also requires preservation.
		before, err := g.Tree(ctx, baseline, "openwiki")
		if err != nil {
			return err
		}
		after, err := g.Tree(ctx, result, "openwiki")
		if err != nil {
			return err
		}
		unchanged = slices.Equal(before, after)
	}
	if !unchanged {
		return checkFailed("The task commit changes openwiki/. Only the Scribe maintains the wiki. Commit project documents outside openwiki/.")
	}
	return nil
}
