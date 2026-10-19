package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestDecodeDBDir(t *testing.T) {
	cases := map[string]string{"blog@002ddb": "blog-db", "shop_db": "shop_db", "a@0020b": "a b"}
	for in, want := range cases {
		if got := decodeDBDir(in); got != want {
			t.Errorf("decodeDBDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReadINIUTF16(t *testing.T) {
	text := "[mysql]\r\nMYSQL_ROOT_PASSWORD=root\r\n[preferences]\r\nDocumentRoot=D:\\laragon\\www\r\n"
	u := utf16.Encode([]rune(text))
	b := []byte{0xFF, 0xFE}
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	p := filepath.Join(t.TempDir(), "laragon.ini")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	ini := readINI(p)
	if ini["mysql_root_password"] != "root" || ini["documentroot"] != `D:\laragon\www` {
		t.Fatalf("readINI = %v", ini)
	}
}

func TestSanitizeDump(t *testing.T) {
	in := "/*M!999999\\- enable the sandbox mode */ \n" +
		"CREATE TABLE t (a text COLLATE utf8mb4_uca1400_ai_ci) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_uca1400_ai_ci;\n" +
		"INSERT INTO t VALUES ('x');\n"
	var out bytes.Buffer
	if err := sanitizeDump(strings.NewReader(in), &out, true); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "sandbox") || strings.Contains(got, "uca1400") || !strings.Contains(got, "COLLATE=utf8mb4_unicode_ci") || !strings.Contains(got, "INSERT INTO t") {
		t.Fatalf("MariaDB -> MySQL:\n%s", got)
	}

	out.Reset()
	if err := sanitizeDump(strings.NewReader("CREATE DATABASE x COLLATE utf8mb4_0900_ai_ci;\n"), &out, false); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "CREATE DATABASE x COLLATE utf8mb4_unicode_ci;\n" {
		t.Fatalf("MySQL -> MariaDB: %q", got)
	}
}

func TestDetectLaragon(t *testing.T) {
	root := filepath.Join(t.TempDir(), "laragon")
	for _, d := range []string{
		"bin/mysql/mysql-8.4.3-winx64/bin", "bin/php/php-8.2.10-Win32-vs16-x64", "bin/php/php-8.3.30-Win32-vs16-x64",
		"data/mysql-8.4/mysql", "data/mysql-8.4/shop_db", "data/mysql-8.4/blog@002ddb", "data/mysql-8.4/sys", "data/mysql-8.4/#innodb_redo",
		"www/shop/public", "www/.git", "usr",
	} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(root, "bin", "mysql", "mysql-8.4.3-winx64", "bin", exeName("mysqld")), nil, 0o755)
	_ = os.WriteFile(filepath.Join(root, "usr", "laragon.ini"), []byte("[mysql]\nMYSQL_ROOT_PASSWORD=secret\n"), 0o644)

	s := detectLaragon(root)
	if s.MySQLBin == "" || filepath.Base(s.DataDir) != "mysql-8.4" || s.Flavor != "mysql" {
		t.Fatalf("mysql: bin=%q data=%q flavor=%q", s.MySQLBin, s.DataDir, s.Flavor)
	}
	if strings.Join(s.Databases, ",") != "blog-db,shop_db" {
		t.Errorf("databases = %v", s.Databases)
	}
	if strings.Join(s.Projects, ",") != "shop" {
		t.Errorf("projects = %v", s.Projects)
	}
	if s.PHP != "8.3.30" || !s.HasPassword || s.password != "secret" {
		t.Errorf("php=%q password=%q", s.PHP, s.password)
	}
	if len(s.Steps) < 6 {
		t.Errorf("steps = %v", s.Steps)
	}
}

func TestRemoveHostsLines(t *testing.T) {
	hosts := filepath.Join(t.TempDir(), "hosts")
	_ = os.WriteFile(hosts, []byte("127.0.0.1 a.local #laragon magic!\r\n127.0.0.1 keep.local\r\n"), 0o644)
	a := &App{}
	n, err := a.removeHostsLines(&Config{HostsFile: hosts}, "#laragon magic!")
	b, _ := os.ReadFile(hosts)
	if err != nil || n != 1 || string(b) != "127.0.0.1 keep.local\r\n" {
		t.Fatalf("n=%d err=%v hosts=%q", n, err, b)
	}
}
