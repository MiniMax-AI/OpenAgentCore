package skills

// VersionDeletionFacts is what a version deletion decides from, loaded under
// the Skill's lock.
type VersionDeletionFacts struct {
	Skill  Skill
	Target Version
	// OthersRemain reports whether the Skill has a version besides Target.
	OthersRemain bool
}

// VersionDeletion is a decided version deletion. DeleteSkill deletes the
// whole Skill, whose only version Target is; otherwise only Target is deleted
// and RefreshLatest moves the latest pointer to the highest remaining version.
type VersionDeletion struct {
	Target        Version
	DeleteSkill   bool
	RefreshLatest bool
}

// DecideVersionDeletion applies the deletion rules. The default version can
// be deleted only as the Skill's sole version, and that deletes the Skill
// itself; Session installations frozen from it are independent copies.
func DecideVersionDeletion(facts VersionDeletionFacts) (VersionDeletion, error) {
	if facts.Target.Version == facts.Skill.DefaultVersion {
		if facts.OthersRemain {
			return VersionDeletion{}, ErrDefaultVersion
		}
		return VersionDeletion{Target: facts.Target, DeleteSkill: true}, nil
	}
	return VersionDeletion{Target: facts.Target, RefreshLatest: facts.Target.Version == facts.Skill.LatestVersion}, nil
}
