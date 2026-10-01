// Package scrivx compares project XML without rewriting the original document.
package scrivx

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"slices"
	"sort"
	"strings"
)

const MaxSize = 8 << 20
const maxDepth = 128
const maxNodes = 100000

var decimalNumber = regexp.MustCompile(`^[+-]?[0-9]+(?:\.[0-9]+)?$`)

type Document struct{ Identifier, Version, Hash string }
type node struct {
	Name       string
	Attributes []attribute
	Text       string
	Children   []*node
}
type attribute struct{ Name, Value string }

func Parse(data []byte) (Document, error) {
	if len(data) > MaxSize {
		return Document{}, errors.New("scrivx превышает лимит 8 МиБ")
	}
	root, err := readXML(data)
	if err != nil {
		return Document{}, err
	}
	id, version := root.attr("Identifier"), root.attr("Version")
	if root.Name != "ScrivenerProject" || id == "" || version != "2.0" {
		return Document{}, errors.New("неподдерживаемый формат scrivx")
	}
	if err := validateBinder(root); err != nil {
		return Document{}, err
	}
	normalize(root, []string{root.Name})
	canonical, err := json.Marshal(root)
	if err != nil {
		return Document{}, err
	}
	hash := sha256.Sum256(canonical)
	return Document{Identifier: id, Version: version, Hash: hex.EncodeToString(hash[:])}, nil
}

func readXML(data []byte) (*node, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var root *node
	var stack []*node
	count := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("XML scrivx: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			count++
			if count > maxNodes || len(stack) >= maxDepth {
				return nil, errors.New("слишком сложный XML scrivx")
			}
			if value.Name.Space != "" {
				return nil, errors.New("пространства имён scrivx не поддерживаются")
			}
			n := &node{Name: value.Name.Local}
			seen := map[string]bool{}
			for _, attr := range value.Attr {
				if attr.Name.Space != "" || seen[attr.Name.Local] {
					return nil, errors.New("неподдерживаемый или повторяющийся атрибут scrivx")
				}
				seen[attr.Name.Local] = true
				n.Attributes = append(n.Attributes, attribute{attr.Name.Local, attr.Value})
			}
			if len(stack) == 0 {
				if root != nil {
					return nil, errors.New("несколько корней XML scrivx")
				}
				root = n
			} else {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, n)
			}
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 {
				if strings.TrimSpace(string(value)) != "" {
					return nil, errors.New("текст вне корня scrivx")
				}
			} else {
				stack[len(stack)-1].Text += string(value)
			}
		case xml.Directive:
			return nil, errors.New("директивы XML scrivx не поддерживаются")
		case xml.ProcInst:
			if value.Target != "xml" || root != nil {
				return nil, errors.New("инструкции XML scrivx не поддерживаются")
			}
		}
	}
	if root == nil || len(stack) != 0 {
		return nil, errors.New("неполный XML scrivx")
	}
	if err := validateText(root); err != nil {
		return nil, err
	}
	return root, nil
}

func validateText(n *node) error {
	if len(n.Children) > 0 {
		if strings.TrimSpace(n.Text) != "" {
			return errors.New("смешанное содержимое XML scrivx не поддерживается")
		}
		n.Text = ""
	}
	for _, child := range n.Children {
		if err := validateText(child); err != nil {
			return err
		}
	}
	return nil
}

func validateBinder(root *node) error {
	var binder *node
	for _, child := range root.Children {
		if child.Name != "Binder" {
			continue
		}
		if binder != nil {
			return errors.New("повторяющийся Binder в scrivx")
		}
		binder = child
	}
	if binder == nil {
		return errors.New("нет Binder в scrivx")
	}
	seen := map[string]bool{}
	var walk func(*node) error
	walk = func(n *node) error {
		if n.Name == "BinderItem" {
			id := n.attr("UUID")
			if id == "" || seen[id] {
				return errors.New("отсутствующий или повторяющийся UUID в scrivx")
			}
			seen[id] = true
		}
		for _, child := range n.Children {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(binder)
}

func (n *node) attr(name string) string {
	for _, attr := range n.Attributes {
		if attr.Name == name {
			return attr.Value
		}
	}
	return ""
}

func normalize(n *node, parts []string) {
	if matchesAny(rules.EmptyContainers, parts) && strings.TrimSpace(n.Text) == "" {
		n.Text = ""
	}
	for _, rule := range rules.IgnoreAttributes {
		if matchPath(rule.Path, parts) {
			n.Attributes = slices.DeleteFunc(n.Attributes, func(a attribute) bool { return slices.Contains(rule.Names, a.Name) })
		}
	}
	sort.Slice(n.Attributes, func(i, j int) bool { return n.Attributes[i].Name < n.Attributes[j].Name })
	children := make([]*node, 0, len(n.Children))
	for _, child := range n.Children {
		childPath := append(slices.Clone(parts), child.Name)
		if matchesAny(rules.IgnoreElements, childPath) && len(child.Attributes) == 0 && len(child.Children) == 0 {
			continue
		}
		normalize(child, childPath)
		if len(child.Children) == 0 && len(child.Attributes) == 0 && child.Text == "" && matchesAny(rules.EmptyContainers, childPath) {
			continue
		}
		children = append(children, child)
	}
	n.Children = children
	if matchesAny(rules.NumericElements, parts) && len(n.Text) <= 128 && decimalNumber.MatchString(n.Text) {
		if number, ok := new(big.Rat).SetString(n.Text); ok {
			n.Text = number.RatString()
		}
	}
	for _, rule := range rules.UnorderedChildren {
		if matchPath(rule.Path, parts) && knownUniqueChildren(children, rule.Names) {
			sort.Slice(children, func(i, j int) bool { return children[i].Name < children[j].Name })
		}
	}
	if len(n.Attributes) == 0 {
		n.Attributes = nil
	}
	if len(n.Children) == 0 {
		n.Children = nil
	}
}

func knownUniqueChildren(children []*node, names []string) bool {
	seen := map[string]bool{}
	for _, child := range children {
		if !slices.Contains(names, child.Name) || seen[child.Name] {
			return false
		}
		seen[child.Name] = true
	}
	return true
}
func matchesAny(patterns []string, parts []string) bool {
	for _, pattern := range patterns {
		if matchPath(pattern, parts) {
			return true
		}
	}
	return false
}
func matchPath(pattern string, parts []string) bool {
	var match func([]string, []string) bool
	match = func(want, have []string) bool {
		if len(want) == 0 {
			return len(have) == 0
		}
		if want[0] == "**" {
			return match(want[1:], have) || (len(have) > 0 && match(want, have[1:]))
		}
		return len(have) > 0 && (want[0] == "*" || want[0] == have[0]) && match(want[1:], have[1:])
	}
	return match(strings.Split(pattern, "/"), parts)
}
