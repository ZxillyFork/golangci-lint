// nolintlint provides a linter to ensure that all //nolint directives are followed by explanations
package nolintlint

import (
	"fmt"
	"go/ast"
	"go/token"
	"regexp"
	"strings"
	"unicode"
)

type BaseIssue struct {
	fullDirective                     string
	directiveWithOptionalLeadingSpace string
	position                          token.Position
}

func (b BaseIssue) Position() token.Position {
	return b.position
}

type ExtraLeadingSpace struct {
	BaseIssue
}

func (i ExtraLeadingSpace) Details() string {
	return fmt.Sprintf("directive `%s` should not have more than one leading space", i.fullDirective)
}

func (i ExtraLeadingSpace) String() string { return toString(i) }

type NotMachine struct {
	BaseIssue
}

func (i NotMachine) Details() string {
	expected := i.fullDirective[:2] + strings.TrimLeftFunc(i.fullDirective[2:], unicode.IsSpace)
	return fmt.Sprintf("directive `%s` should be written without leading space as `%s`",
		i.fullDirective, expected)
}

func (i NotMachine) String() string { return toString(i) }

type NotSpecific struct {
	BaseIssue
}

func (i NotSpecific) Details() string {
	return fmt.Sprintf("directive `%s` should mention specific linter such as `%s:my-linter`",
		i.fullDirective, i.directiveWithOptionalLeadingSpace)
}

func (i NotSpecific) String() string { return toString(i) }

type ParseError struct {
	BaseIssue
}

func (i ParseError) Details() string {
	return fmt.Sprintf("directive `%s` should match `%s[:<comma-separated-linters>] [// <explanation>]`",
		i.fullDirective,
		i.directiveWithOptionalLeadingSpace)
}

func (i ParseError) String() string { return toString(i) }

type NoExplanation struct {
	BaseIssue
	fullDirectiveWithoutExplanation string
}

func (i NoExplanation) Details() string {
	return fmt.Sprintf("directive `%s` should provide explanation such as `%s // this is why`",
		i.fullDirective, i.fullDirectiveWithoutExplanation)
}

func (i NoExplanation) String() string { return toString(i) }

type UnusedCandidate struct {
	BaseIssue
	ExpectedLinter string
}

func (i UnusedCandidate) Details() string {
	details := fmt.Sprintf("directive `%s` is unused", i.fullDirective)
	if i.ExpectedLinter != "" {
		details += fmt.Sprintf(" for linter %s", i.ExpectedLinter)
	}
	return details
}

func (i UnusedCandidate) String() string { return toString(i) }

func toString(i Issue) string {
	return fmt.Sprintf("%s at %s", i.Details(), i.Position())
}

type Issue interface {
	Details() string
	Position() token.Position
	String() string
}

type Needs uint

const (
	NeedsMachineOnly Needs = 1 << iota
	NeedsSpecific
	NeedsExplanation
	NeedsUnused
	NeedsAll = NeedsMachineOnly | NeedsSpecific | NeedsExplanation
)

var commentPattern = regexp.MustCompile(`^//\s*(nolint)(:\s*[\w-]+\s*(?:,\s*[\w-]+\s*)*)?\b`)

// matches a complete nolint directive
var fullDirectivePattern = regexp.MustCompile(`^//\s*nolint(:\s*[\w-]+\s*(?:,\s*[\w-]+\s*)*)?\s*(//.*)?\s*\n?$`)

type Linter struct {
	excludes        []string // lists individual linters that don't require explanations
	needs           Needs    // indicates which linter checks to perform
	excludeByLinter map[string]bool
}

// NewLinter creates a linter that enforces that the provided directives fulfill the provided requirements
func NewLinter(needs Needs, excludes []string) (*Linter, error) {
	excludeByName := make(map[string]bool)
	for _, e := range excludes {
		excludeByName[e] = true
	}

	return &Linter{
		needs:           needs,
		excludeByLinter: excludeByName,
	}, nil
}

var leadingSpacePattern = regexp.MustCompile(`^//(\s*)`)
var trailingBlankExplanation = regexp.MustCompile(`\s*(//\s*)?$`)

func (l Linter) Run(fset *token.FileSet, nodes ...ast.Node) ([]Issue, error) {
	var issues []Issue

	for _, node := range nodes {
		file, ok := node.(*ast.File)
		if !ok {
			continue
		}

		for _, c := range file.Comments {
			issues = append(issues, l.commentAnalysis(fset, c)...)
		}
	}

	return issues, nil
}

func (l Linter) commentAnalysis(fset *token.FileSet, c *ast.CommentGroup) []Issue {
	var issues []Issue

	for _, comment := range c.List {
		if !commentPattern.MatchString(comment.Text) {
			continue
		}

		// check for a space between the "//" and the directive
		leadingSpaceMatches := leadingSpacePattern.FindStringSubmatch(comment.Text)

		var leadingSpace string
		if len(leadingSpaceMatches) > 0 {
			leadingSpace = leadingSpaceMatches[1]
		}

		parts := strings.SplitN(strings.SplitN(comment.Text, "//", 3)[1], ":", 2)

		if len(parts) > 1 {
			for _, s := range strings.Split(parts[1], ",") {
				if strings.TrimSpace(s) == "nolintlint" {
					return nil
				}
			}
		}

		directiveWithOptionalLeadingSpace := comment.Text
		if len(leadingSpace) > 0 {
			directiveWithOptionalLeadingSpace = "// " + strings.TrimSpace(parts[0])
		}

		base := BaseIssue{
			fullDirective:                     comment.Text,
			directiveWithOptionalLeadingSpace: directiveWithOptionalLeadingSpace,
			position:                          fset.Position(comment.Pos()),
		}

		// check for, report and eliminate leading spaces so we can check for other issues
		if len(leadingSpace) > 1 {
			issues = append(issues, ExtraLeadingSpace{BaseIssue: base})
		}

		if (l.needs&NeedsMachineOnly) != 0 && len(leadingSpace) > 0 {
			issues = append(issues, NotMachine{BaseIssue: base})
		}

		fullMatches := fullDirectivePattern.FindStringSubmatch(comment.Text)
		if len(fullMatches) == 0 {
			issues = append(issues, ParseError{BaseIssue: base})
			continue
		}

		lintersText, explanation := fullMatches[1], fullMatches[2]
		var linters []string
		if len(lintersText) > 0 {
			lls := strings.Split(lintersText[1:], ",")
			linters = make([]string, 0, len(lls))
			for _, ll := range lls {
				ll = strings.TrimSpace(ll)
				if ll != "" {
					linters = append(linters, ll)
				}
			}
		}

		if (l.needs & NeedsSpecific) != 0 {
			if len(linters) == 0 {
				issues = append(issues, NotSpecific{BaseIssue: base})
			}
		}

		// when detecting unused directives, we send all the directives through and filter them out in the nolint processor
		if (l.needs & NeedsUnused) != 0 {
			if len(linters) == 0 {
				issues = append(issues, UnusedCandidate{BaseIssue: base})
			} else {
				for _, linter := range linters {
					issues = append(issues, UnusedCandidate{BaseIssue: base, ExpectedLinter: linter})
				}
			}
		}

		if (l.needs&NeedsExplanation) != 0 && (explanation == "" || strings.TrimSpace(explanation) == "//") {
			needsExplanation := len(linters) == 0 // if no linters are mentioned, we must have explanation
			// otherwise, check if we are excluding all of the mentioned linters
			for _, ll := range linters {
				if !l.excludeByLinter[ll] { // if a linter does require explanation
					needsExplanation = true
					break
				}
			}

			if needsExplanation {
				fullDirectiveWithoutExplanation := trailingBlankExplanation.ReplaceAllString(comment.Text, "")
				issues = append(issues, NoExplanation{
					BaseIssue:                       base,
					fullDirectiveWithoutExplanation: fullDirectiveWithoutExplanation,
				})
			}
		}
	}

	return issues
}
