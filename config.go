// This file owns everything about deciding "how do we talk to the daemon?".
// It merges four sources of configuration into a single Config struct:
//
//   1. Explicit command-line flags (highest priority)
//   2. Environment variables
//   3. gridcoinresearch.conf, if present
//   4. Built-in defaults baked into the binary (lowest priority)
//
// The cascade is important: you can always override the conf file with env
// vars without editing it, and override env vars with a flag for a one-off
// run. Missing conf file is NEVER a fatal error — the TUI must still work
// remotely against a daemon on another host where there's no local conf.
package main

import (
	"bufio" // line-by-line scanner for the conf file
	"cmp"   // Go 1.22 "cmp.Or" — returns the first non-zero arg in a list
	"flag"  // standard library flag parser
	"fmt"
	"os"
	"path/filepath" // joins paths with the right separator on every OS
	"strings"
	"time"
)

// Built-in defaults. The ports come from gridcoinresearchd's chainparamsbase.cpp
// (mainnet RPC port 15715, testnet RPC port 25715).
const (
	defaultMainnetPort = "15715"
	defaultTestnetPort = "25715"
	defaultHost        = "127.0.0.1"
	defaultRefresh     = 10 * time.Second
)

// Config is the fully-resolved connection + behaviour settings the rest of
// the program consumes. It is a plain data struct — no methods hide state.
type Config struct {
	Testnet     bool
	Host        string
	Port        string
	User        string
	Password    string
	Refresh     time.Duration
	ConfPath    string // the conf file we actually read (for display in the config panel); "" if none
	NetworkName string // "mainnet" or "testnet" — handy for rendering
}

// URL builds the JSON-RPC endpoint. Method receivers with a lowercase name
// (c) and no pointer are "value receivers": they get a copy of Config, so
// they cannot mutate it. That makes Config feel like an immutable value.
func (c Config) URL() string {
	return fmt.Sprintf("http://%s:%s/", c.Host, c.Port)
}

// confValues holds the handful of keys we care about from the wallet conf
// file. It is unexported (lowercase name) because nothing outside this file
// should need to see it.
type confValues struct {
	rpcuser     string
	rpcpassword string
	rpcport     string
	rpcconnect  string
}

// LoadConfig is the single entry point used by main.go. It returns the
// (Config, error) pair — Go functions conventionally return an error as the
// last value instead of throwing exceptions.
func LoadConfig(args []string) (Config, error) {
	// We build a fresh FlagSet instead of using the package-level flag.Parse
	// so tests can call LoadConfig with synthetic argv without interfering
	// with the global flag state.
	fs := flag.NewFlagSet("gridcoinresearch-tui", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	// Each fs.String/fs.Bool returns a pointer. Dereferencing it later with
	// *testnet / *hostFlag gives the value the user supplied (or the default).
	var (
		testnet     = fs.Bool("testnet", false, "use testnet conf path + default port")
		mainnet     = fs.Bool("mainnet", false, "use mainnet (default)")
		hostFlag    = fs.String("rpc-host", "", "RPC host (default 127.0.0.1)")
		portFlag    = fs.String("rpc-port", "", "RPC port (default 15715 mainnet / 25715 testnet)")
		userFlag    = fs.String("rpc-user", "", "RPC username (optional)")
		passFlag    = fs.String("rpc-password", "", "RPC password (optional; prefer GRC_RPC_PASSWORD env var)")
		confFlag    = fs.String("conf", "", "path to gridcoinresearch.conf (optional)")
		refreshFlag = fs.Duration("refresh", defaultRefresh, "refresh interval")
	)

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	if *testnet && *mainnet {
		return Config{}, fmt.Errorf("--testnet and --mainnet are mutually exclusive")
	}

	cfg := Config{
		Testnet: *testnet,
		Refresh: *refreshFlag,
	}
	if cfg.Testnet {
		cfg.NetworkName = "testnet"
	} else {
		cfg.NetworkName = "mainnet"
	}

	// ----- layer 3: conf file (best-effort) -----
	confPath := *confFlag
	if confPath == "" {
		confPath = defaultConfPath(cfg.Testnet)
	}
	vals := readConfFile(confPath)
	if vals != nil {
		cfg.ConfPath = confPath
	} else if cfg.Testnet {
		// Users often keep a single conf in the mainnet path even when running testnet.
		if v := readConfFile(defaultConfPath(false)); v != nil {
			vals = v
			cfg.ConfPath = defaultConfPath(false)
		}
	}
	// If we never found a conf file, pretend we got an empty one so the
	// cmp.Or cascade below has a struct to read from instead of a nil pointer.
	if vals == nil {
		vals = &confValues{}
	}

	// cmp.Or walks its arguments left-to-right and returns the first one that
	// is NOT the zero value ("" for strings). That is exactly the priority
	// cascade we want: flag → env → conf → built-in default.
	cfg.Host = cmp.Or(*hostFlag, os.Getenv("GRC_RPC_HOST"), vals.rpcconnect, defaultHost)
	cfg.Port = cmp.Or(*portFlag, os.Getenv("GRC_RPC_PORT"), vals.rpcport, defaultPort(cfg.Testnet))
	// No final fallback for credentials on purpose: empty user/password is a
	// valid, first-class case (many local wallets run without auth).
	cfg.User = cmp.Or(*userFlag, os.Getenv("GRC_RPC_USER"), vals.rpcuser)
	cfg.Password = cmp.Or(*passFlag, os.Getenv("GRC_RPC_PASSWORD"), vals.rpcpassword)

	return cfg, nil
}

func defaultPort(testnet bool) string {
	if testnet {
		return defaultTestnetPort
	}
	return defaultMainnetPort
}

// defaultConfPath returns ~/.GridcoinResearch/gridcoinresearch.conf for mainnet
// or ~/.GridcoinResearch/testnet/gridcoinresearch.conf for testnet. An empty
// string is returned if we can't even figure out the home directory.
func defaultConfPath(testnet bool) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	if testnet {
		return filepath.Join(home, ".GridcoinResearch", "testnet", "gridcoinresearch.conf")
	}
	return filepath.Join(home, ".GridcoinResearch", "gridcoinresearch.conf")
}

// readConfFile parses a gridcoinresearch.conf file. The file format is the
// same `key=value` plaintext format that bitcoind uses. We only extract the
// handful of keys the TUI cares about and silently ignore everything else.
//
// Returns nil if the file can't be opened (missing, permission denied, etc.).
// Missing conf is NOT an error — the caller treats nil as "no values, fall
// through to env/defaults".
func readConfFile(path string) *confValues {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	// defer runs the close when the function returns, no matter which path
	// we take out. It is Go's equivalent of Python's "with" block.
	defer f.Close()

	v := &confValues{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue // skip blank lines and comments
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue // not a key=value line, skip
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		switch key {
		case "rpcuser":
			v.rpcuser = val
		case "rpcpassword":
			v.rpcpassword = val
		case "rpcport":
			v.rpcport = val
		case "rpcconnect":
			v.rpcconnect = val
		}
	}
	return v
}
