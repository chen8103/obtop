package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

const (
	defaultHost     = "127.0.0.1"
	defaultPort     = 3306
	defaultDatabase = "oceanbase"
	defaultInterval = time.Second
)

type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	Interval time.Duration
}

func Parse(args []string, stderr io.Writer) (Config, error) {
	cfg := Config{}
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "-help") {
		WriteUsage(stderr)
		return Config{}, flag.ErrHelp
	}

	flagSet := flag.NewFlagSet("obtop", flag.ContinueOnError)
	flagSet.SetOutput(stderr)
	flagSet.Usage = func() { WriteUsage(stderr) }

	var intervalSeconds int
	flagSet.StringVar(&cfg.Host, "h", defaultHost, "")
	flagSet.IntVar(&cfg.Port, "P", defaultPort, "")
	flagSet.StringVar(&cfg.User, "u", "", "")
	flagSet.StringVar(&cfg.Password, "p", "", "")
	flagSet.StringVar(&cfg.Database, "d", defaultDatabase, "")
	flagSet.IntVar(&intervalSeconds, "i", int(defaultInterval.Seconds()), "")

	if err := flagSet.Parse(args); err != nil {
		return Config{}, err
	}

	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.User = strings.TrimSpace(cfg.User)
	cfg.Database = strings.TrimSpace(cfg.Database)
	cfg.Interval = time.Duration(intervalSeconds) * time.Second

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	switch {
	case c.Host == "":
		return errors.New("host is required")
	case c.Port <= 0:
		return errors.New("port must be greater than 0")
	case c.User == "":
		return errors.New("user is required")
	case c.Password == "":
		return errors.New("password is required")
	case c.Database == "":
		return errors.New("database is required")
	case c.Interval <= 0:
		return errors.New("interval must be greater than 0")
	default:
		return nil
	}
}

func (c Config) Address() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

func (c Config) DSN() string {
	mysqlCfg := mysql.NewConfig()
	mysqlCfg.Net = "tcp"
	mysqlCfg.Addr = c.Address()
	mysqlCfg.User = c.User
	mysqlCfg.Passwd = c.Password
	mysqlCfg.DBName = c.Database
	mysqlCfg.Timeout = 5 * time.Second
	mysqlCfg.ReadTimeout = 5 * time.Second
	mysqlCfg.WriteTimeout = 5 * time.Second
	mysqlCfg.Params = map[string]string{
		"charset": "utf8mb4",
	}
	return mysqlCfg.FormatDSN()
}

func (c Config) HeaderTitle() string {
	return fmt.Sprintf("obtop - OceanBase Activity Monitor (%s/%s, every %s)", c.Address(), c.Database, c.Interval)
}

func ExitWithUsage(err error) {
	if err == nil {
		os.Exit(0)
	}
	fmt.Fprintln(os.Stderr, err)
	WriteUsage(os.Stderr)
	os.Exit(2)
}

func WriteUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: obtop [options]")
	fmt.Fprintln(w, "       obtop version | --version")
	fmt.Fprintln(w, "       obtop snapshot|watch|sql-history|explain [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Options:")
	fmt.Fprintf(w, "  -h host      OBProxy address (default: %s)\n", defaultHost)
	fmt.Fprintf(w, "  -P port      Port (default: %d)\n", defaultPort)
	fmt.Fprintln(w, "  -u user      Username, supports user or user@tenant (required)")
	fmt.Fprintln(w, "  -p password  Password (required)")
	fmt.Fprintf(w, "  -d database  Database (default: %s)\n", defaultDatabase)
	fmt.Fprintf(w, "  -i interval  Refresh interval in seconds (default: %.0f)\n", defaultInterval.Seconds())
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Machine commands (JSON/NDJSON on stdout, logs on stderr):")
	fmt.Fprintln(w, "  obtop snapshot --format json")
	fmt.Fprintln(w, "  obtop watch --interval 1s --duration 60s --format ndjson")
	fmt.Fprintln(w, "  obtop sql-history --since 5m --format ndjson")
	fmt.Fprintln(w, "  obtop sql-history --all --limit 500 --format ndjson")
	fmt.Fprintln(w, "  obtop sql-history --cursor 10.0.0.1:2882/99 --format ndjson")
	fmt.Fprintln(w, "  obtop explain --sql 'SELECT 1' --format json")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Examples:")
	fmt.Fprintln(w, "  obtop -h")
	fmt.Fprintln(w, "  obtop -h 10.0.0.1 -P 3306 -u dba -p xxxx")
	fmt.Fprintln(w, "  obtop -h 10.0.0.1 -P 3306 -u dba@my_tenant -p xxxx")
}
