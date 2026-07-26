package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/xenking/dummypage/internal/courses"
)

func main() {
	config, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "usage: courses-data --input <export.json> [--input <export.json>...] [--input-dir <exports-dir>] --output <catalog.json.gz> [--torrent-dir <dir>]")
		fmt.Fprintln(os.Stderr, "   or: courses-data <telegram-export.json> <catalog.json.gz> [torrent-dir]")
		os.Exit(2)
	}
	if err := buildFiles(config.InputPaths, config.OutputPath, config.TorrentDir); err != nil {
		fmt.Fprintln(os.Stderr, "build courses catalog:", err)
		os.Exit(1)
	}
}

type config struct {
	InputPaths []string
	OutputPath string
	TorrentDir string
}

type repeatedStrings []string

func (values *repeatedStrings) String() string {
	return strings.Join(*values, ",")
}

func (values *repeatedStrings) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("empty input path")
	}
	*values = append(*values, value)
	return nil
}

func parseArgs(args []string) (config, error) {
	if len(args) == 2 || len(args) == 3 {
		if !strings.HasPrefix(args[0], "-") {
			result := config{InputPaths: []string{args[0]}, OutputPath: args[1]}
			if len(args) == 3 {
				result.TorrentDir = args[2]
			}
			return result, nil
		}
	}

	flags := flag.NewFlagSet("courses-data", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var inputs repeatedStrings
	var inputDir string
	result := config{}
	flags.Var(&inputs, "input", "source export JSON path")
	flags.StringVar(&inputDir, "input-dir", "", "directory containing source export JSON files")
	flags.StringVar(&result.OutputPath, "output", "", "catalog JSON gzip output path")
	flags.StringVar(&result.TorrentDir, "torrent-dir", "", "directory containing downloaded .torrent files")
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if flags.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	result.InputPaths = append(result.InputPaths, inputs...)
	if strings.TrimSpace(inputDir) != "" {
		paths, err := inputPathsFromDir(inputDir)
		if err != nil {
			return config{}, err
		}
		result.InputPaths = append(result.InputPaths, paths...)
	}
	if len(result.InputPaths) == 0 {
		return config{}, fmt.Errorf("at least one --input or --input-dir is required")
	}
	if strings.TrimSpace(result.OutputPath) == "" {
		return config{}, fmt.Errorf("--output is required")
	}
	return result, nil
}

func inputPathsFromDir(inputDir string) ([]string, error) {
	entries, err := os.ReadDir(inputDir)
	if err != nil {
		return nil, fmt.Errorf("read input directory: %w", err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		paths = append(paths, filepath.Join(inputDir, entry.Name()))
	}
	slices.Sort(paths)
	return paths, nil
}

func buildFile(inputPath, outputPath, torrentDir string) error {
	return buildFiles([]string{inputPath}, outputPath, torrentDir)
}

func buildFiles(inputPaths []string, outputPath, torrentDir string) error {
	inputs := make([]courses.SourceInput, 0, len(inputPaths))
	closers := make([]io.Closer, 0, len(inputPaths))
	defer func() {
		for _, closer := range closers {
			_ = closer.Close()
		}
	}()
	for _, inputPath := range inputPaths {
		input, err := os.Open(inputPath)
		if err != nil {
			return fmt.Errorf("open source export %q: %w", inputPath, err)
		}
		closers = append(closers, input)
		inputs = append(inputs, courses.SourceInput{Reader: input, Name: inputPath})
	}

	outputDir := filepath.Dir(outputPath)
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	temp, err := os.CreateTemp(outputDir, ".courses-catalog-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary catalog: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	stats, buildErr := courses.BuildGzipFromSources(inputs, temp, torrentDir)
	closeErr := temp.Close()
	if buildErr != nil {
		return buildErr
	}
	if closeErr != nil {
		return fmt.Errorf("close temporary catalog: %w", closeErr)
	}
	if err := os.Chmod(tempPath, 0o600); err != nil {
		return fmt.Errorf("set catalog permissions: %w", err)
	}
	if err := os.Rename(tempPath, outputPath); err != nil {
		return fmt.Errorf("publish catalog: %w", err)
	}

	fmt.Printf(
		"built %s: %d courses from %d source entries, %d normalized titles, %d links, %d passwords\n",
		outputPath,
		stats.Entries,
		stats.SourceEntries,
		stats.NormalizedTitles,
		stats.Links,
		stats.Passwords,
	)
	return nil
}
