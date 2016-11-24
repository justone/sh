// Copyright (c) 2016, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package syntax

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/kr/pretty"
)

func TestParseComments(t *testing.T) {
	in := "# foo\ncmd\n# bar"
	want := &File{
		Comments: []*Comment{
			{Text: " foo"},
			{Text: " bar"},
		},
		Stmts: litStmts("cmd"),
	}
	singleParse(in, want, ParseComments)(t)
}

func TestParseBash(t *testing.T) {
	t.Parallel()
	n := 1
	for i, c := range append(fileTests, fileTestsNoPrint...) {
		for _, in := range c.Strs {
			ioutil.WriteFile(fmt.Sprintf("../corpus/parser-%03d", n), []byte(in), 0644)
			n++
		}
		want := c.Bash
		if want == nil {
			continue
		}
		for j, in := range c.Strs {
			t.Run(fmt.Sprintf("%03d-%d", i, j), singleParse(in, want, 0))
		}
	}
}

func TestParsePosix(t *testing.T) {
	t.Parallel()
	for i, c := range append(fileTests, fileTestsNoPrint...) {
		want := c.Posix
		if want == nil {
			continue
		}
		for j, in := range c.Strs {
			t.Run(fmt.Sprintf("%03d-%d", i, j),
				singleParse(in, want, PosixConformant))
		}
	}
}

func TestMain(m *testing.M) {
	bashVersion, bashError = checkBash()
	os.Exit(m.Run())
}

var (
	bashVersion int
	bashError   error
)

func checkBash() (int, error) {
	out, err := exec.Command("bash", "-c", "echo -n $BASH_VERSION").Output()
	if err != nil {
		return 0, err
	}
	got := string(out)
	versions := []string{
		"4.2",
		"4.3",
		"4.4",
	}
	vercodes := []int{
		42,
		43,
		44,
	}
	for i, ver := range versions {
		if strings.HasPrefix(got, ver) {
			return vercodes[i], nil
		}
	}
	return 0, fmt.Errorf("need bash %s, found %s", strings.Join(versions, "/"), got)
}

func confirmParse(in string, min int, posix, fail bool) func(*testing.T) {
	return func(t *testing.T) {
		if bashVersion < min {
			t.Skip("need bash%d, have bash%d", min, bashVersion)
			return
		}
		t.Parallel()
		var opts []string
		if posix {
			opts = append(opts, "--posix")
		}
		if strings.Contains(in, "#INVBASH") {
			fail = !fail
		}
		if strings.Contains(in, "@(") {
			// otherwise bash refuses to parse these
			// properly. Also avoid -n since that too makes
			// bash bail.
			in = "shopt -s extglob\n" + in
		} else if !fail {
			// -n makes bash accept invalid inputs like
			// "let" or "`{`", so only use it in
			// non-erroring tests. Should be safe to not use
			// -n anyway since these are supposed to just
			// fail.
			// also, -n will break if we are using extglob
			// as extglob is not actually applied.
			opts = append(opts, "-n")
		}
		cmd := exec.Command("bash", opts...)
		cmd.Stdin = strings.NewReader(in)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		if stderr.Len() > 0 {
			// bash sometimes likes to error on an input via stderr
			// while forgetting to set the exit code to non-zero.
			// Fun.
			if s := stderr.String(); !strings.Contains(s, ": warning: ") {
				err = errors.New(s)
			}
		}
		if fail && err == nil {
			t.Fatalf("Expected error in `%s` of %q, found none", strings.Join(cmd.Args, " "), in)
		} else if !fail && err != nil {
			t.Fatalf("Unexpected error in `%s` of %q: %v", strings.Join(cmd.Args, " "), in, err)
		}
	}
}

func TestParseBashConfirm(t *testing.T) {
	if testing.Short() {
		t.Skip("calling bash is slow.")
	}
	if bashError != nil {
		t.Skip(bashError)
	}
	for i, c := range append(fileTests, fileTestsNoPrint...) {
		for j, in := range c.Strs {
			t.Run(fmt.Sprintf("%03d-%d", i, j),
				confirmParse(in, c.minBash, false, false))
		}
	}
}

func TestParseErrBashConfirm(t *testing.T) {
	if testing.Short() {
		t.Skip("calling bash is slow.")
	}
	if bashError != nil {
		t.Skip(bashError)
	}
	for i, c := range append(shellTests, bashTests...) {
		t.Run(fmt.Sprintf("%03d", i), confirmParse(c.in, 0, false, true))
	}
}

func TestParseErrPosixConfirm(t *testing.T) {
	if testing.Short() {
		t.Skip("calling bash is slow.")
	}
	if bashError != nil {
		t.Skip(bashError)
	}
	for i, c := range append(shellTests, posixTests...) {
		t.Run(fmt.Sprintf("%03d", i), confirmParse(c.in, 0, true, true))
	}
}

func singleParse(in string, want *File, mode ParseMode) func(t *testing.T) {
	return func(t *testing.T) {
		got, err := Parse([]byte(in), "", mode)
		if err != nil {
			t.Fatalf("Unexpected error in %q: %v", in, err)
		}
		checkNewlines(t, in, got.Lines)
		got.Lines = nil
		setPosRecurse(t, "", want, 0, false)
		setPosRecurse(t, in, got, 0, true)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("AST mismatch in %q\ndiff:\n%s", in,
				strings.Join(pretty.Diff(want, got), "\n"),
			)
		}
	}
}

func BenchmarkParse(b *testing.B) {
	type benchmark struct {
		name, in string
	}
	benchmarks := []benchmark{
		{
			"LongStrs",
			strings.Repeat("\n\n\t\t        \n", 10) +
				"# " + strings.Repeat("foo bar ", 10) + "\n" +
				strings.Repeat("longlit_", 10) + "\n" +
				"'" + strings.Repeat("foo bar ", 20) + "'\n" +
				`"` + strings.Repeat("foo bar ", 20) + `"`,
		},
		{
			"Cmds+Nested",
			strings.Repeat("a b c d; ", 8) +
				"a() { (b); { c; }; }; $(d; `e`)",
		},
		{
			"Vars+Clauses",
			"foo=bar a=b c=d$foo${bar}e $simple ${complex:-default}; " +
				"if a; then while b; do for c in d e; do f; done; done; fi",
		},
		{
			"Binary+Redirs",
			"a | b && c || d | e && g || f | h; " +
				"foo >a <b <<<c 2>&1 <<EOF\n" +
				strings.Repeat("somewhat long heredoc line\n", 10) +
				"EOF",
		},
	}
	for _, c := range benchmarks {
		b.Run(c.name, func(b *testing.B) {
			in := []byte(c.in)
			for i := 0; i < b.N; i++ {
				if _, err := Parse(in, "", ParseComments); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

type errorCase struct {
	in, want string
}

var shellTests = []errorCase{
	{
		"'",
		`1:1: reached EOF without closing quote '`,
	},
	{
		`"`,
		`1:1: reached EOF without closing quote "`,
	},
	{
		`'\''`,
		`1:4: reached EOF without closing quote '`,
	},
	{
		";",
		`1:1: ; can only immediately follow a statement`,
	},
	{
		"{ ; }",
		`1:3: ; can only immediately follow a statement`,
	},
	{
		`"foo"(){}`,
		`1:1: invalid func name: "\"foo\""`,
	},
	{
		`foo$bar(){}`,
		`1:1: invalid func name: "foo$bar"`,
	},
	{
		"{",
		`1:1: reached EOF without matching { with }`,
	},
	{
		"}",
		`1:1: } can only be used to close a block`,
	},
	{
		"{ #}",
		`1:1: reached EOF without matching { with }`,
	},
	{
		"(",
		`1:1: reached EOF without matching ( with )`,
	},
	{
		")",
		`1:1: ) can only be used to close a subshell`,
	},
	{
		"`",
		"1:1: reached EOF without closing quote `",
	},
	{
		";;",
		`1:1: ;; can only be used in a case clause`,
	},
	{
		"( foo;",
		`1:1: reached EOF without matching ( with )`,
	},
	{
		"&",
		`1:1: & can only immediately follow a statement`,
	},
	{
		"|",
		`1:1: | can only immediately follow a statement`,
	},
	{
		"&&",
		`1:1: && can only immediately follow a statement`,
	},
	{
		"||",
		`1:1: || can only immediately follow a statement`,
	},
	{
		"foo; || bar",
		`1:6: || can only immediately follow a statement`,
	},
	{
		"foo & || bar",
		`1:7: || can only immediately follow a statement`,
	},
	{
		"foo & ; bar",
		`1:7: ; can only immediately follow a statement`,
	},
	{
		"foo;;",
		`1:4: ;; can only be used in a case clause`,
	},
	{
		"foo(",
		`1:1: "foo(" must be followed by )`,
	},
	{
		"foo(bar",
		`1:1: "foo(" must be followed by )`,
	},
	{
		"à(",
		`1:1: "foo(" must be followed by )`,
	},
	{
		"foo'",
		`1:4: reached EOF without closing quote '`,
	},
	{
		`foo"`,
		`1:4: reached EOF without closing quote "`,
	},
	{
		`"foo`,
		`1:1: reached EOF without closing quote "`,
	},
	{
		`"foobar\`,
		`1:1: reached EOF without closing quote "`,
	},
	{
		`"foo\a`,
		`1:1: reached EOF without closing quote "`,
	},
	{
		"foo()",
		`1:1: "foo()" must be followed by a statement`,
	},
	{
		"foo() {",
		`1:7: reached EOF without matching { with }`,
	},
	{
		"echo foo(",
		`1:9: a command can only contain words and redirects`,
	},
	{
		"foo &&",
		`1:5: && must be followed by a statement`,
	},
	{
		"foo |",
		`1:5: | must be followed by a statement`,
	},
	{
		"foo ||",
		`1:5: || must be followed by a statement`,
	},
	{
		"foo >",
		`1:5: > must be followed by a word`,
	},
	{
		"foo >>",
		`1:5: >> must be followed by a word`,
	},
	{
		"foo <",
		`1:5: < must be followed by a word`,
	},
	{
		"foo 2>",
		`1:6: > must be followed by a word`,
	},
	{
		"foo <\nbar",
		`2:1: redirect word must be on the same line`,
	},
	{
		"foo <<",
		`1:5: << must be followed by a word`,
	},
	{
		"foo <<\nEOF\nbar\nEOF",
		`2:1: heredoc stop word must be on the same line`,
	},
	{
		"if",
		`1:1: "if" must be followed by a statement list`,
	},
	{
		"if foo;",
		`1:1: "if <cond>" must be followed by "then"`,
	},
	{
		"if foo then",
		`1:1: "if <cond>" must be followed by "then"`,
	},
	{
		"if foo; then bar;",
		`1:1: if statement must end with "fi"`,
	},
	{
		"if foo; then bar; fi#etc",
		`1:1: if statement must end with "fi"`,
	},
	{
		"if a; then b; elif c;",
		`1:15: "elif <cond>" must be followed by "then"`,
	},
	{
		"'foo' '",
		`1:7: reached EOF without closing quote '`,
	},
	{
		"'foo\n' '",
		`2:3: reached EOF without closing quote '`,
	},
	{
		"while",
		`1:1: "while" must be followed by a statement list`,
	},
	{
		"while foo;",
		`1:1: "while <cond>" must be followed by "do"`,
	},
	{
		"while foo; do bar",
		`1:1: while statement must end with "done"`,
	},
	{
		"while foo; do bar;",
		`1:1: while statement must end with "done"`,
	},
	{
		"until",
		`1:1: "until" must be followed by a statement list`,
	},
	{
		"until foo;",
		`1:1: "until <cond>" must be followed by "do"`,
	},
	{
		"until foo; do bar",
		`1:1: until statement must end with "done"`,
	},
	{
		"until foo; do bar;",
		`1:1: until statement must end with "done"`,
	},
	{
		"for",
		`1:1: "for" must be followed by a literal`,
	},
	{
		"for i",
		`1:1: "for foo" must be followed by "in", ; or a newline`,
	},
	{
		"for i in;",
		`1:1: "for foo [in words]" must be followed by "do"`,
	},
	{
		"for i in 1 2 3;",
		`1:1: "for foo [in words]" must be followed by "do"`,
	},
	{
		"for i in 1 2 &",
		`1:14: word list can only contain words`,
	},
	{
		"for i in 1 2 3; do echo $i;",
		`1:1: for statement must end with "done"`,
	},
	{
		"for i in 1 2 3; echo $i;",
		`1:1: "for foo [in words]" must be followed by "do"`,
	},
	{
		"for 'i' in 1 2 3; do echo $i; done",
		`1:1: "for" must be followed by a literal`,
	},
	{
		"for in 1 2 3; do echo $i; done",
		`1:1: "for foo" must be followed by "in", ; or a newline`,
	},
	{
		"foo &\n;",
		`2:1: ; can only immediately follow a statement`,
	},
	{
		"echo $(foo",
		`1:6: reached EOF without matching ( with )`,
	},
	{
		"echo $((foo",
		`1:6: reached EOF without matching $(( with ))`,
	},
	{
		`foo $((\`,
		`1:5: reached EOF without matching $(( with ))`,
	},
	{
		`fo $((o\`,
		`1:4: reached EOF without matching $(( with ))`,
	},
	{
		`echo $((foo\a`,
		`1:6: reached EOF without matching $(( with ))`,
	},
	{
		`$(("`,
		`1:1: reached EOF without matching $(( with ))`,
	},
	{
		`$((a"`,
		`1:1: reached EOF without matching $(( with ))`,
	},
	{
		`$(($((a"`,
		`1:1: reached EOF without matching $(( with ))`,
	},
	{
		`$(('`,
		`1:1: reached EOF without matching $(( with ))`,
	},
	{
		`$((& $(`,
		`1:1: reached EOF without matching $(( with ))`,
	},
	{
		`$((& 0 $(`,
		`1:1: reached EOF without matching $(( with ))`,
	},
	{
		`$((a'`,
		`1:1: reached EOF without matching $(( with ))`,
	},
	{
		`$((a b"`,
		`1:1: reached EOF without matching $(( with ))`,
	},
	{
		`$((a"'`,
		`1:1: reached EOF without matching $(( with ))`,
	},
	{
		"$((\"`)",
		`1:1: reached EOF without matching $(( with ))`,
	},
	{
		"$((\"a`b((",
		`1:1: reached EOF without matching $(( with ))`,
	},
	{
		"echo $((()))",
		`1:9: parentheses must enclose an expression`,
	},
	{
		"echo $(((3))",
		`1:6: reached EOF without matching $(( with ))`,
	},
	{
		"echo $((+))",
		`1:9: + must be followed by an expression`,
	},
	{
		"echo $((a b c))",
		`1:11: not a valid arithmetic operator: b`,
	},
	{
		"echo $((a ; c))",
		`1:11: not a valid arithmetic operator: ;`,
	},
	{
		"echo $((a *))",
		`1:11: * must be followed by an expression`,
	},
	{
		"echo $((++))",
		`1:9: ++ must be followed by an expression`,
	},
	{
		"<<EOF\n$(()a",
		`2:1: reached EOF without matching $(( with ))`,
	},
	{
		"<<EOF\n`))",
		`2:2: ) can only be used to close a subshell`,
	},
	{
		"echo ${foo",
		`1:6: reached EOF without matching ${ with }`,
	},
	{
		"echo $foo ${}",
		`1:11: parameter expansion requires a literal`,
	},
	{
		"echo ${foo-bar",
		`1:6: reached EOF without matching ${ with }`,
	},
	{
		"#foo\n{",
		`2:1: reached EOF without matching { with }`,
	},
	{
		`echo "foo${bar"`,
		`1:10: reached EOF without matching ${ with }`,
	},
	{
		"foo\n;",
		`2:1: ; can only immediately follow a statement`,
	},
	{
		"(foo) bar",
		`1:7: statements must be separated by &, ; or a newline`,
	},
	{
		"{ foo; } bar",
		`1:10: statements must be separated by &, ; or a newline`,
	},
	{
		"if foo; then bar; fi bar",
		`1:22: statements must be separated by &, ; or a newline`,
	},
	{
		"case",
		`1:1: "case" must be followed by a word`,
	},
	{
		"case i",
		`1:1: "case x" must be followed by "in"`,
	},
	{
		"case i in 3) foo;",
		`1:1: case statement must end with "esac"`,
	},
	{
		"case i in 3) foo; 4) bar; esac",
		`1:20: a command can only contain words and redirects`,
	},
	{
		"case i in 3&) foo;",
		`1:12: case patterns must be separated with |`,
	},
	{
		"case $i in &) foo;",
		`1:12: case patterns must consist of words`,
	},
	{
		"\"`\"",
		`1:3: reached EOF without closing quote "`,
	},
	{
		"`\"`",
		"1:3: reached EOF without closing quote `",
	},
	{
		"`{\n`",
		"1:2: reached ` without matching { with }",
	},
	{
		"echo \"`)`\"",
		`1:8: ) can only be used to close a subshell`,
	},
	{
		"foo <<$(bar)",
		`1:7: nested statements not allowed in heredoc words`,
	},
	{
		"foo <<`bar`",
		`1:7: nested statements not allowed in heredoc words`,
	},
	{
		`""()`,
		`1:1: invalid func name: "\"\""`,
	},
	{
		// bash errors on the empty condition here, this is to
		// add coverage for empty statement lists
		`if; then bar; fi; ;`,
		`1:19: ; can only immediately follow a statement`,
	},
}

func checkError(in, want string, mode ParseMode) func(*testing.T) {
	return func(t *testing.T) {
		_, err := Parse([]byte(in), "", mode)
		if err == nil {
			t.Fatalf("Expected error in %q: %v", in, want)
		}
		if got := err.Error(); got != want {
			t.Fatalf("Error mismatch in %q\nwant: %s\ngot:  %s",
				in, want, got)
		}
	}
}

func TestParseErrPosix(t *testing.T) {
	t.Parallel()
	for i, c := range append(shellTests, posixTests...) {
		t.Run(fmt.Sprintf("%03d", i), checkError(c.in, c.want, PosixConformant))
	}
}

func TestParseErrBash(t *testing.T) {
	t.Parallel()
	for i, c := range append(shellTests, bashTests...) {
		t.Run(fmt.Sprintf("%03d", i), checkError(c.in, c.want, 0))
	}
}

var bashTests = []errorCase{
	{
		"((foo",
		`1:1: reached EOF without matching (( with ))`,
	},
	{
		"echo ((foo",
		`1:6: a command can only contain words and redirects`,
	},
	{
		"foo |&",
		`1:5: |& must be followed by a statement`,
	},
	{
		"let",
		`1:1: let clause requires at least one expression`,
	},
	{
		"let a+ b",
		`1:6: + must be followed by an expression`,
	},
	{
		"let + a",
		`1:5: + must be followed by an expression`,
	},
	{
		"let a ++",
		`1:7: ++ must be followed by an expression`,
	},
	{
		"let a+\n",
		`1:6: + must be followed by an expression`,
	},
	{
		"let ))",
		`1:1: let clause requires at least one expression`,
	},
	{
		"`let !`",
		`1:6: ! must be followed by an expression`,
	},
	{
		"let 'foo'\n'",
		`2:1: reached EOF without closing quote '`,
	},
	{
		"[[",
		`1:1: test clause requires at least one expression`,
	},
	{
		"[[ ]]",
		`1:1: test clause requires at least one expression`,
	},
	{
		"[[ a",
		`1:1: reached EOF without matching [[ with ]]`,
	},
	{
		"[[ -f a",
		`1:1: reached EOF without matching [[ with ]]`,
	},
	{
		"[[ a -nt b",
		`1:1: reached EOF without matching [[ with ]]`,
	},
	{
		"[[ a =~ b",
		`1:1: reached EOF without matching [[ with ]]`,
	},
	{
		"[[ a b c ]]",
		`1:6: not a valid test operator: b`,
	},
	{
		"[[ a & b ]]",
		`1:6: not a valid test operator: &`,
	},
	{
		"[[ true && () ]]",
		`1:12: parentheses must enclose an expression`,
	},
	{
		"local (",
		`1:7: "local" must be followed by words`,
	},
	{
		"declare 0=${o})",
		`1:15: statements must be separated by &, ; or a newline`,
	},
	{
		"a=(<)",
		`1:4: array elements must be words`,
	},
	{
		"function",
		`1:1: "function" must be followed by a word`,
	},
	{
		"function foo(",
		`1:10: "foo(" must be followed by )`,
	},
	{
		"function `function",
		`1:11: "function" must be followed by a word`,
	},
	{
		`function "foo"(){}`,
		`1:10: invalid func name: "\"foo\""`,
	},
	{
		"function foo()",
		`1:1: "foo()" must be followed by a statement`,
	},
	{
		"foo <<<",
		`1:5: <<< must be followed by a word`,
	},
	{
		"echo $[foo",
		`1:6: reached EOF without matching $[ with ]`,
	},
	{
		"echo $'",
		`1:6: reached EOF without closing quote '`,
	},
	{
		`echo $"`,
		`1:6: reached EOF without closing quote "`,
	},
	{
		"echo @(",
		`1:6: reached EOF without matching @( with )`,
	},
	{
		"echo @(a",
		`1:6: reached EOF without matching @( with )`,
	},
	{
		"coproc",
		`1:1: coproc clause requires a command`,
	},
	{
		"coproc\n$",
		`1:1: coproc clause requires a command`,
	},
	{
		"coproc declare (",
		`1:16: "declare" must be followed by words`,
	},
	{
		"`let` { foo; }",
		`1:2: let clause requires at least one expression`,
	},
	{
		"echo ${foo[1 2]}",
		`1:14: not a valid arithmetic operator: 2`,
	},
	{
		"echo ${foo[}",
		`1:11: [ must be followed by an expression`,
	},
	{
		"echo ${foo[]}",
		`1:11: [ must be followed by an expression`,
	},
	{
		"echo ${a/\n",
		`1:6: reached EOF without matching ${ with }`,
	},
	{
		"echo ${a-\n",
		`1:6: reached EOF without matching ${ with }`,
	},
	{
		"echo ${foo:",
		`1:11: : must be followed by an expression`,
	},
	{
		"echo ${foo:1 2} #INVBASH lazy eval",
		`1:14: not a valid arithmetic operator: 2`,
	},
	{
		"echo ${foo:1",
		`1:6: reached EOF without matching ${ with }`,
	},
	{
		"echo ${foo:1:",
		`1:13: : must be followed by an expression`,
	},
	{
		"echo ${foo:1:2",
		`1:6: reached EOF without matching ${ with }`,
	},
}

var posixTests = []errorCase{
	{
		"((foo",
		`1:2: reached EOF without matching ( with )`,
	},
	{
		"echo ((foo",
		`1:1: "foo(" must be followed by )`,
	},
	{
		"function foo() { bar; } #INVBASH --posix is wrong",
		`1:13: a command can only contain words and redirects`,
	},
	{
		"foo <(",
		`1:5: < must be followed by a word`,
	},
	{
		"foo >(",
		`1:5: > must be followed by a word`,
	},
	{
		"foo |&",
		`1:5: | must be followed by a statement`,
	},
	{
		"foo ;&",
		`1:6: & can only immediately follow a statement`,
	},
	{
		"foo ;;&",
		`1:5: ;; can only be used in a case clause`,
	},
	{
		"echo !(a)",
		`1:7: a command can only contain words and redirects`,
	},
	{
		"foo=(1 2) #INVBASH --posix is wrong",
		`1:5: arrays are a bash feature`,
	},
	{
		"echo ${foo[1]} #INVBASH --posix is wrong",
		`1:11: arrays are a bash feature`,
	},
	{
		"echo ${foo/a/b} #INVBASH --posix is wrong",
		`1:11: search and replace is a bash feature`,
	},
	{
		"echo ${foo:1} #INVBASH --posix is wrong",
		`1:11: slicing is a bash feature`,
	},
	{
		"echo ${foo,bar} #INVBASH --posix is wrong",
		`1:11: case expansions are a bash feature`,
	},
}

func TestInputName(t *testing.T) {
	in := shellTests[0].in
	want := "some-file.sh:" + shellTests[0].want
	_, err := Parse([]byte(in), "some-file.sh", 0)
	if err == nil {
		t.Fatalf("Expected error in %q: %v", in, want)
	}
	got := err.Error()
	if got != want {
		t.Fatalf("Error mismatch in %q\nwant: %s\ngot:  %s",
			in, want, got)
	}
}
