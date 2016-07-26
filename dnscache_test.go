package main

import (
	"bufio"
	"io/ioutil"
	"os"
	"testing"
)

func openfile(t *testing.T, path string) *os.File {
	f, err := os.OpenFile(path, os.O_RDWR, 0666)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func tmpfile(t *testing.T, prefix string) (*os.File, func()) {
	f, err := ioutil.TempFile("", prefix)
	if err != nil {
		t.Fatal(err)
	}
	return f, func() { f.Close(); os.Remove(f.Name()) }
}

func TestMain(t *testing.T) {
	file, teardown := tmpfile(t, "dnscache-stats")
	defer teardown()

	os.Args = []string{"dnscache-stats", "-tai64",
		"-i", "30s", "-v",
		file.Name(),
	}

	os.Stdin = openfile(t, "testdata/dnscache.log")

	fd1 := os.Stdout
	os.Stdout = openfile(t, os.DevNull)
	main()
	os.Stdout = fd1

	file.Sync()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		t.Logf("%s", scanner.Text())
	}
}
