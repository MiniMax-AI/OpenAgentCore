package codex

import "context"

func setSkillExtraRoots(ctx context.Context, rpc *JSONRPCClient, roots []string) error {
	if len(roots) == 0 {
		return nil
	}
	_, err := rpc.Request(ctx, "skills/extraRoots/set", SkillsExtraRootsSetParams{ExtraRoots: roots})
	return err
}
