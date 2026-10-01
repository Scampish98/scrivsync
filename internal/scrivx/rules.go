package scrivx

// XML paths use ** for any nesting depth. Unknown fields remain significant.
var rules = comparisonRules{
	IgnoreAttributes: []attributeRule{
		{"ScrivenerProject", []string{"Modified", "ModID", "Creator", "Device"}},
		{"ScrivenerProject/ProjectTargets/PreviousSession", []string{"Date"}},
		{"ScrivenerProject/ProjectTargets/SessionTarget", []string{"NextResetDate"}},
	},
	IgnoreElements: []string{
		"ScrivenerProject/Binder/**/BinderItem/TextSettings/TextSelection",
		"ScrivenerProject/Binder/**/BinderItem/CorkboardAndOutliner/SelectedSubdocumentUUIDs",
	},
	EmptyContainers: []string{
		"ScrivenerProject/Binder/**/BinderItem/TextSettings",
		"ScrivenerProject/Binder/**/BinderItem/MetaData",
		"ScrivenerProject/Binder/**/BinderItem/CorkboardAndOutliner",
	},
	NumericElements: []string{
		"ScrivenerProject/Binder/**/BinderItem/MediaSettings/ImageScaleFactor",
	},
	UnorderedChildren: []childOrderRule{
		{"ScrivenerProject", []string{"Binder", "Collections", "SectionTypes", "LabelSettings", "StatusSettings", "ProjectBookmarks", "ProjectTargets", "RecentWritingHistory", "TemplateFolderUUID", "BookmarksFolderUUID", "PrintSettings"}},
		{"ScrivenerProject/Binder/**/BinderItem/MetaData", []string{"IncludeInCompile", "IconFileName", "ShowSynopsisImage", "SectionType", "LabelID", "StatusID", "Keywords"}},
	},
}

type attributeRule struct {
	Path  string
	Names []string
}
type childOrderRule struct {
	Path  string
	Names []string
}
type comparisonRules struct {
	IgnoreAttributes  []attributeRule
	IgnoreElements    []string
	EmptyContainers   []string
	NumericElements   []string
	UnorderedChildren []childOrderRule
}
