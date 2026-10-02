// Package ploopy carries the documents the binary ships with, so an installed
// ploopy works in a repository that has none of its own.
package ploopy

import _ "embed"

// PlanUnitSkill is how a session carries out one unit. A repository may
// override it; see config.SkillPath.
//
//go:embed skills/plan-unit/SKILL.md
var PlanUnitSkill string

// PlanWritingSkill is how a plan the loop can run is written.
//
//go:embed skills/plan-writing/SKILL.md
var PlanWritingSkill string
