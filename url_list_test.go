package main

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
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

func TestReorderArgsAcceptsFlagsAfterSitemap(t *testing.T) {
	got := reorderArgs([]string{"https://example.com/sitemap.xml", "--url", "https://example.com/extra", "--quiet"})
	want := []string{"--url", "https://example.com/extra", "--quiet", "https://example.com/sitemap.xml"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reorderArgs() = %#v, want %#v", got, want)
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
