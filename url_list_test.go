package main

import (
	"context"
	"flag"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReadURLListSkipsBlankAndComments(t *testing.T) {
	got, err := readURLList(strings.NewReader("\n # comment\nexample.com/a\n  https://example.com/b  \n#another\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://example.com/a", "https://example.com/b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("readURLList() = %#v, want %#v", got, want)
	}
}

func TestLoadListURLsExplicitAndFileOrdering(t *testing.T) {
	list := t.TempDir() + "/urls.txt"
	if err := writeTestFile(list, "file.example/one\nhttps://file.example/two\n"); err != nil {
		t.Fatal(err)
	}
	got, err := loadListURLs([]string{"explicit.example/a", "https://explicit.example/b"}, list, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"https://explicit.example/a", "https://explicit.example/b",
		"https://file.example/one", "https://file.example/two",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadListURLs() = %#v, want %#v", got, want)
	}
}

func TestLoadListURLsStdin(t *testing.T) {
	got, err := loadListURLs(nil, "-", strings.NewReader("# ignored\nstdin.example/a\n\nhttps://stdin.example/b\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://stdin.example/a", "https://stdin.example/b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadListURLs(stdin) = %#v, want %#v", got, want)
	}
}

func TestForwardURLSourcesStandaloneList(t *testing.T) {
	out := make(chan string, 2)
	if !forwardURLSources(context.Background(), nil, []string{"standalone.example/a", "https://standalone.example/b"}, out, nil) {
		t.Fatal("forwardURLSources returned false")
	}
	close(out)
	got := collect(out)
	want := []string{"https://standalone.example/a", "https://standalone.example/b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("forwardURLSources(standalone) = %#v, want %#v", got, want)
	}
}

func TestForwardURLSourcesCombinedOrdering(t *testing.T) {
	sitemap := make(chan string, 2)
	sitemap <- "https://sitemap.example/one"
	sitemap <- "https://sitemap.example/two"
	close(sitemap)
	out := make(chan string, 4)
	list := []string{"list.example/one", "https://list.example/two"}
	if !forwardURLSources(context.Background(), sitemap, list, out, nil) {
		t.Fatal("forwardURLSources returned false")
	}
	close(out)
	got := collect(out)
	want := []string{
		"https://sitemap.example/one", "https://sitemap.example/two",
		"https://list.example/one", "https://list.example/two",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("forwardURLSources() = %#v, want %#v", got, want)
	}
}

func newTestFlagSet() (*flag.FlagSet, *int, *time.Duration, *bool, *repeatableString) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	concurrency := fs.Int("c", 20, "")
	timeout := fs.Duration("timeout", 10*time.Second, "")
	quiet := fs.Bool("quiet", false, "")
	var urls repeatableString
	fs.Var(&urls, "url", "")
	return fs, concurrency, timeout, quiet, &urls
}

func TestParseInterspersedFlagsAfterSitemap(t *testing.T) {
	fs, _, _, quiet, urls := newTestFlagSet()
	positionals, err := parseInterspersed(fs, []string{"https://example.com/sitemap.xml", "--url", "https://example.com/extra", "--quiet"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"https://example.com/sitemap.xml"}; !reflect.DeepEqual(positionals, want) {
		t.Fatalf("positionals = %#v, want %#v", positionals, want)
	}
	if !*quiet {
		t.Fatal("--quiet not parsed")
	}
	if want := (repeatableString{"https://example.com/extra"}); !reflect.DeepEqual(*urls, want) {
		t.Fatalf("--url = %#v, want %#v", *urls, want)
	}
}

func TestParseInterspersedEqualsForm(t *testing.T) {
	fs, concurrency, _, _, _ := newTestFlagSet()
	positionals, err := parseInterspersed(fs, []string{"--c=30", "https://example.com/sitemap.xml"})
	if err != nil {
		t.Fatal(err)
	}
	if *concurrency != 30 {
		t.Fatalf("--c=30 parsed as %d", *concurrency)
	}
	if want := []string{"https://example.com/sitemap.xml"}; !reflect.DeepEqual(positionals, want) {
		t.Fatalf("positionals = %#v, want %#v", positionals, want)
	}
}

func TestParseInterspersedDoubleDashTerminator(t *testing.T) {
	fs, _, _, _, _ := newTestFlagSet()
	positionals, err := parseInterspersed(fs, []string{"--quiet", "--", "-weird"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"-weird"}; !reflect.DeepEqual(positionals, want) {
		t.Fatalf("positionals = %#v, want %#v", positionals, want)
	}
}

// The F1 regression: spellings the flag package natively accepts (double-dash
// short form, single-dash long form) must not consume the following flag as
// their value.
func TestParseInterspersedAlternateFlagSpellings(t *testing.T) {
	t.Run("double-dash short form", func(t *testing.T) {
		fs, concurrency, _, quiet, _ := newTestFlagSet()
		positionals, err := parseInterspersed(fs, []string{"--c", "30", "--quiet", "https://example.com/"})
		if err != nil {
			t.Fatal(err)
		}
		if *concurrency != 30 || !*quiet {
			t.Fatalf("--c/--quiet parsed as %d/%v", *concurrency, *quiet)
		}
		if want := []string{"https://example.com/"}; !reflect.DeepEqual(positionals, want) {
			t.Fatalf("positionals = %#v, want %#v", positionals, want)
		}
	})
	t.Run("single-dash long form", func(t *testing.T) {
		fs, _, timeout, quiet, _ := newTestFlagSet()
		positionals, err := parseInterspersed(fs, []string{"-timeout", "5s", "--quiet", "https://example.com/"})
		if err != nil {
			t.Fatal(err)
		}
		if *timeout != 5*time.Second || !*quiet {
			t.Fatalf("-timeout/--quiet parsed as %v/%v", *timeout, *quiet)
		}
		if want := []string{"https://example.com/"}; !reflect.DeepEqual(positionals, want) {
			t.Fatalf("positionals = %#v, want %#v", positionals, want)
		}
	})
}

func TestParseInterspersedRepeatedFlagsAroundPositionals(t *testing.T) {
	fs, _, _, _, urls := newTestFlagSet()
	positionals, err := parseInterspersed(fs, []string{"--url", "https://example.com/a", "https://example.com/sitemap.xml", "--url", "https://example.com/b"})
	if err != nil {
		t.Fatal(err)
	}
	if want := (repeatableString{"https://example.com/a", "https://example.com/b"}); !reflect.DeepEqual(*urls, want) {
		t.Fatalf("--url = %#v, want %#v", *urls, want)
	}
	if want := []string{"https://example.com/sitemap.xml"}; !reflect.DeepEqual(positionals, want) {
		t.Fatalf("positionals = %#v, want %#v", positionals, want)
	}
}

func TestParseInterspersedValueFlagAsFinalArgument(t *testing.T) {
	fs, _, _, _, _ := newTestFlagSet()
	if _, err := parseInterspersed(fs, []string{"--quiet", "--c"}); err == nil {
		t.Fatal("expected an error for a value flag without a value")
	}
}

func TestParseInterspersedPositionalBetweenValueFlags(t *testing.T) {
	fs, concurrency, timeout, _, _ := newTestFlagSet()
	positionals, err := parseInterspersed(fs, []string{"--c", "30", "https://example.com/sitemap.xml", "--timeout", "5s"})
	if err != nil {
		t.Fatal(err)
	}
	if *concurrency != 30 || *timeout != 5*time.Second {
		t.Fatalf("flags parsed as %d/%v", *concurrency, *timeout)
	}
	if want := []string{"https://example.com/sitemap.xml"}; !reflect.DeepEqual(positionals, want) {
		t.Fatalf("positionals = %#v, want %#v", positionals, want)
	}
}

func TestReadURLListRejectsOverlongLine(t *testing.T) {
	// One line beyond the 1 MiB scanner limit must fail predictably.
	_, err := readURLList(strings.NewReader(strings.Repeat("a", (1<<20)+1)))
	if err == nil {
		t.Fatal("expected a scanner error for a line over 1 MiB")
	}
}

func TestReadURLListCRLF(t *testing.T) {
	got, err := readURLList(strings.NewReader("crlf.example/one\r\nhttps://crlf.example/two\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://crlf.example/one", "https://crlf.example/two"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("readURLList(CRLF) = %#v, want %#v", got, want)
	}
}

func TestReadURLListMidLineHashNotStripped(t *testing.T) {
	// Pin current behavior: only lines starting with # are comments; a
	// mid-line # stays part of the URL.
	got, err := readURLList(strings.NewReader("hash.example/a#frag\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://hash.example/a#frag"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("readURLList() = %#v, want %#v", got, want)
	}
}

func TestReadURLListFileMissingContainsPath(t *testing.T) {
	missing := t.TempDir() + "/does-not-exist.txt"
	_, err := readURLListFile(missing, nil)
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("error %q does not contain path %q", err, missing)
	}
}

func TestReadURLListFileDirectoryErrors(t *testing.T) {
	if _, err := readURLListFile(t.TempDir(), nil); err == nil {
		t.Fatal("expected an error when --urls names a directory")
	}
}

func TestReadURLListFileStdinFallback(t *testing.T) {
	// --urls - with a nil reader falls back to os.Stdin.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("fallback.example/stdin\n"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	original := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = original; r.Close() })
	got, err := readURLListFile("-", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://fallback.example/stdin"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("readURLListFile(-, nil) = %#v, want %#v", got, want)
	}
}

func TestLoadListURLsFileErrorAbortsAfterExplicit(t *testing.T) {
	missing := t.TempDir() + "/missing.txt"
	got, err := loadListURLs([]string{"explicit.example/a"}, missing, nil)
	if err == nil {
		t.Fatal("expected the file leg error to abort loadListURLs")
	}
	if got != nil {
		t.Fatalf("loadListURLs returned %#v alongside the error", got)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("error %q does not contain path %q", err, missing)
	}
}

func TestEmitURLListNormalizesSchemeLessURLs(t *testing.T) {
	out := make(chan string, 2)
	if !emitURLList(context.Background(), out, []string{"example.test/path", "https://example.test/secure"}, nil) {
		t.Fatal("emitURLList returned false")
	}
	close(out)
	got := collect(out)
	want := []string{"https://example.test/path", "https://example.test/secure"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("emitURLList() = %#v, want %#v", got, want)
	}
}

func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}
