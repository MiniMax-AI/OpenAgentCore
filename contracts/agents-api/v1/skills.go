package v1

// Skill describes a tenant-owned, versioned bundle without exposing its contents.
type Skill struct {
	ID             string `json:"id" binding:"required"`
	Object         string `json:"object" binding:"required" enums:"skill"`
	CreatedAt      int64  `json:"created_at" binding:"required"`
	Name           string `json:"name" binding:"required"`
	Description    string `json:"description" binding:"required"`
	DefaultVersion string `json:"default_version" binding:"required"`
	LatestVersion  string `json:"latest_version" binding:"required"`
}

type SkillVersion struct {
	ID          string `json:"id" binding:"required"`
	Object      string `json:"object" binding:"required" enums:"skill.version"`
	CreatedAt   int64  `json:"created_at" binding:"required"`
	SkillID     string `json:"skill_id" binding:"required"`
	Version     string `json:"version" binding:"required"`
	Name        string `json:"name" binding:"required"`
	Description string `json:"description" binding:"required"`
}

type SkillList struct {
	Object  string  `json:"object" binding:"required" enums:"list"`
	Data    []Skill `json:"data" binding:"required"`
	FirstID *string `json:"first_id" extensions:"x-nullable"`
	LastID  *string `json:"last_id" extensions:"x-nullable"`
	HasMore bool    `json:"has_more" binding:"required"`
}

type SkillVersionList struct {
	Object  string         `json:"object" binding:"required" enums:"list"`
	Data    []SkillVersion `json:"data" binding:"required"`
	FirstID *string        `json:"first_id" extensions:"x-nullable"`
	LastID  *string        `json:"last_id" extensions:"x-nullable"`
	HasMore bool           `json:"has_more" binding:"required"`
}

type SkillDeleted struct {
	ID      string `json:"id" binding:"required"`
	Object  string `json:"object" binding:"required" enums:"skill.deleted"`
	Deleted bool   `json:"deleted" binding:"required"`
}

type SkillVersionDeleted struct {
	ID      string `json:"id" binding:"required"`
	Object  string `json:"object" binding:"required" enums:"skill.version.deleted"`
	Version string `json:"version" binding:"required"`
	Deleted bool   `json:"deleted" binding:"required"`
}

type SkillUpdateRequest struct {
	DefaultVersion string `json:"default_version" binding:"required"`
}
