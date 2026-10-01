package scrivx

import (
	"strings"
	"testing"
)

func sample(body string) string {
	return `<ScrivenerProject Identifier="project-id" Version="2.0" Modified="yesterday" Creator="Mac" Device="mac" ModID="one"><Binder>` + body + `</Binder></ScrivenerProject>`
}
func item(id, title, extra string) string {
	return `<BinderItem UUID="` + id + `" Type="Text"><Title>` + title + `</Title>` + extra + `</BinderItem>`
}
func hash(t *testing.T, text string) string {
	t.Helper()
	doc, err := Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return doc.Hash
}

func TestOnlyKnownInsignificantChangesAreEqual(t *testing.T) {
	base := sample(item("a", "Chapter", ``))
	for name, changed := range map[string]string{
		"save metadata":     strings.NewReplacer("yesterday", "today", "Mac", "Windows", "mac", "pc", "one", "two").Replace(base),
		"selection":         sample(item("a", "Chapter", `<TextSettings><TextSelection>200,5</TextSelection></TextSettings>`)),
		"empty metadata":    sample(item("a", "Chapter", `<MetaData>  </MetaData>`)),
		"selected document": sample(item("a", "Chapter", `<CorkboardAndOutliner><SelectedSubdocumentUUIDs>x</SelectedSubdocumentUUIDs></CorkboardAndOutliner>`)),
		"attribute order":   strings.ReplaceAll(base, `UUID="a" Type="Text"`, `Type="Text" UUID="a"`),
		"indentation":       strings.ReplaceAll(base, `><`, ">\n  <"),
	} {
		t.Run(name, func(t *testing.T) {
			if hash(t, base) != hash(t, changed) {
				t.Fatal("insignificant change conflicts")
			}
		})
	}
	left := sample(item("a", "Chapter", `<MetaData><IncludeInCompile>Yes</IncludeInCompile><IconFileName>Book</IconFileName></MetaData><MediaSettings><ImageScaleFactor>1.0</ImageScaleFactor></MediaSettings>`))
	right := sample(item("a", "Chapter", `<MetaData><IconFileName>Book</IconFileName><IncludeInCompile>Yes</IncludeInCompile></MetaData><MediaSettings><ImageScaleFactor>1</ImageScaleFactor></MediaSettings>`))
	if hash(t, left) != hash(t, right) {
		t.Fatal("equivalent XML representation conflicts")
	}
}

func TestMeaningfulAndUnknownChangesRemainSignificant(t *testing.T) {
	base := sample(item("a", "Chapter", ``) + item("b", "Other", ``))
	for name, changed := range map[string]string{
		"title":                  strings.ReplaceAll(base, "Chapter", "Renamed"),
		"title spaces":           strings.ReplaceAll(base, "Chapter", " Chapter "),
		"order":                  sample(item("b", "Other", ``) + item("a", "Chapter", ``)),
		"delete":                 sample(item("a", "Chapter", ``)),
		"move":                   sample(item("b", "Other", `<Children>`+item("a", "Chapter", ``)+`</Children>`)),
		"compile":                strings.Replace(base, "</Title>", `</Title><MetaData><IncludeInCompile>No</IncludeInCompile></MetaData>`, 1),
		"unknown text setting":   strings.Replace(base, "</Title>", `</Title><TextSettings><Target>500</Target></TextSettings>`, 1),
		"unknown root attribute": strings.Replace(base, "<ScrivenerProject ", `<ScrivenerProject FutureOption="yes" `, 1),
		"unknown field":          strings.Replace(base, "</Title>", `</Title><FutureSetting>x</FutureSetting>`, 1),
		"statistics":             strings.Replace(base, "</ScrivenerProject>", `<RecentWritingHistory><Day Words="500"/></RecentWritingHistory></ScrivenerProject>`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if hash(t, base) == hash(t, changed) {
				t.Fatal("meaningful change ignored")
			}
		})
	}
}

func TestNestedSelectionAndUnknownOrder(t *testing.T) {
	base := sample(item("parent", "Folder", `<Children>`+item("child", "Chapter", ``)+`</Children>`))
	changed := strings.Replace(base, `<Title>Chapter</Title>`, `<Title>Chapter</Title><TextSettings><TextSelection>9,0</TextSelection></TextSettings>`, 1)
	if hash(t, base) != hash(t, changed) {
		t.Fatal("nested selection conflicts")
	}
	left := sample(item("a", "Chapter", `<MetaData><UnknownA>1</UnknownA><UnknownB>2</UnknownB></MetaData>`))
	right := sample(item("a", "Chapter", `<MetaData><UnknownB>2</UnknownB><UnknownA>1</UnknownA></MetaData>`))
	if hash(t, left) == hash(t, right) {
		t.Fatal("unknown child order ignored")
	}
}

func TestUnsupportedXMLDoesNotProduceSemanticHash(t *testing.T) {
	for name, text := range map[string]string{
		"malformed":      `<ScrivenerProject>`,
		"duplicate UUID": sample(item("a", "First", ``) + item("a", "Second", ``)),
		"future version": strings.Replace(sample(""), `Version="2.0"`, `Version="3.0"`, 1),
		"mixed content":  sample(item("a", "Chapter", `<Unknown>before<Child/>after</Unknown>`)),
		"namespaces":     strings.Replace(sample(""), `<ScrivenerProject `, `<ScrivenerProject xmlns="unknown" `, 1),
		"directive":      `<!DOCTYPE test>` + sample(""),
		"deep":           sample(strings.Repeat("<Folder>", 130) + strings.Repeat("</Folder>", 130)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(text)); err == nil {
				t.Fatal("unsupported XML accepted")
			}
		})
	}
}

func TestSessionClockIgnoredButStatisticsProtected(t *testing.T) {
	base := strings.Replace(sample(""), "</ScrivenerProject>", `<ProjectTargets><SessionTarget NextResetDate="tomorrow">100</SessionTarget><PreviousSession Date="today" Words="10" Characters="50"/></ProjectTargets></ScrivenerProject>`, 1)
	changed := strings.NewReplacer("tomorrow", "next week", "today", "later").Replace(base)
	if hash(t, base) != hash(t, changed) {
		t.Fatal("session clock update conflicts")
	}
	changed = strings.Replace(base, `Words="10"`, `Words="11"`, 1)
	if hash(t, base) == hash(t, changed) {
		t.Fatal("session statistics ignored")
	}
	changed = strings.Replace(base, `>100<`, `>200<`, 1)
	if hash(t, base) == hash(t, changed) {
		t.Fatal("writing target ignored")
	}
}

func TestUnknownSelectionFieldsAreProtected(t *testing.T) {
	base := sample(item("a", "Chapter", `<TextSettings><TextSelection>0,0</TextSelection></TextSettings>`))
	changed := strings.Replace(base, "<TextSelection>", `<TextSelection FutureSetting="yes">`, 1)
	if hash(t, base) == hash(t, changed) {
		t.Fatal("unknown selection attribute ignored")
	}
}
