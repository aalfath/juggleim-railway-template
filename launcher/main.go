// jim-railway: small launcher for JuggleIM on Railway.
//
// It renders /opt/conf/config.yml from environment variables, loads the
// upstream schema into an empty MySQL database, sets the console "admin"
// password from JUGGLEIM_ADMIN_PASSWORD, publishes the public WebSocket
// address to client SDKs, starts /opt/imserver and serves every public path
// on one port ($PORT) with a path-based reverse proxy.
package main

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-sql-driver/mysql"
)

const (
	wsPort    = 9003 // /im, /imbot, /health, /version
	apiPort   = 9001 // /apigateway (signed server API)
	navPort   = 9002 // /navigator (SDK bootstrap)
	adminPort = 8090 // admin console + /admingateway
)

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func must(cond bool, msg string) {
	if !cond {
		log.Fatalf("[jim-railway] %s", msg)
	}
}

func q(s string) string { b, _ := json.Marshal(s); return string(b) } // JSON string = valid YAML scalar

func main() {
	log.SetFlags(0)
	adminPass := os.Getenv("JUGGLEIM_ADMIN_PASSWORD")
	adminSecret := os.Getenv("JUGGLEIM_ADMIN_SECRET")
	must(len(adminPass) >= 12, "JUGGLEIM_ADMIN_PASSWORD must be set (12+ characters); refusing to start with a weak or default console password")
	must(len(adminSecret) >= 32, "JUGGLEIM_ADMIN_SECRET must be set (32+ characters); without it upstream signs console sessions with a public default key")

	host := env("MYSQLHOST", "")
	must(host != "", "MYSQLHOST is not set (reference your MySQL service)")
	port := env("MYSQLPORT", "3306")
	user := env("MYSQLUSER", "root")
	pass := os.Getenv("MYSQLPASSWORD")
	dbName := env("MYSQLDATABASE", "railway")
	addr := host + ":" + port
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") { // bare IPv6
		addr = "[" + host + "]:" + port
	}
	dataDir := env("JUGGLEIM_DATA_DIR", "/opt/data")
	_ = os.MkdirAll(dataDir+"/kvdb", 0o755)
	_ = os.MkdirAll("/opt/conf", 0o755)
	_ = os.MkdirAll("/opt/logs", 0o755)

	cfg := fmt.Sprintf(`nodeName: jim-node-1
nodeHost: 127.0.0.1
msgStoreEngine: mysql
log:
  logPath: ./logs
  logName: jim-info
kvdb:
  isOpen: true
  dataPath: %s
msglogs:
  logPath: %s
mysql:
  user: %s
  password: %s
  address: %s
  name: %s
connectManager:
  wsPort: %d
apiGateway:
  httpPort: %d
navGateway:
  httpPort: %d
adminGateway:
  httpPort: %d
adminSecret: %s
imApiDomain: http://127.0.0.1:%d
`, q(dataDir+"/kvdb"), q(dataDir), q(user), q(pass), q(addr), q(dbName), wsPort, apiPort, navPort, adminPort, q(adminSecret), apiPort)
	must(os.WriteFile("/opt/conf/config.yml", []byte(cfg), 0o600) == nil, "cannot write /opt/conf/config.yml")

	prepareDB(user, pass, addr, dbName, adminPass)

	cmd := exec.Command("/opt/imserver")
	cmd.Dir = "/opt"
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	must(cmd.Start() == nil, "cannot start /opt/imserver")
	log.Printf("[jim-railway] imserver started (pid %d)", cmd.Process.Pid)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		s := <-sigs
		log.Printf("[jim-railway] %v received, stopping imserver", s)
		_ = cmd.Process.Signal(syscall.SIGTERM)
		time.Sleep(15 * time.Second)
		os.Exit(0)
	}()

	go serveProxy(env("PORT", "8080"))

	err := cmd.Wait()
	log.Printf("[jim-railway] imserver exited: %v", err)
	os.Exit(1) // let Railway's restart policy bring it back
}

func prepareDB(user, pass, addr, dbName, adminPass string) {
	c := mysql.NewConfig()
	c.User, c.Passwd, c.Net, c.Addr, c.DBName = user, pass, "tcp", addr, dbName
	c.ParseTime = true
	c.Timeout = 10 * time.Second
	db, err := sql.Open("mysql", c.FormatDSN())
	must(err == nil, "bad MySQL settings")
	defer db.Close()

	deadline := time.Now().Add(5 * time.Minute)
	for {
		if err = db.Ping(); err == nil {
			break
		}
		must(time.Now().Before(deadline), "MySQL not reachable after 5 minutes: "+err.Error())
		log.Printf("[jim-railway] waiting for MySQL at %s: %v", addr, err)
		time.Sleep(3 * time.Second)
	}

	var t string
	err = db.QueryRow("SHOW TABLES LIKE 'globalconfs'").Scan(&t)
	if err == sql.ErrNoRows {
		log.Printf("[jim-railway] empty database: loading the upstream schema")
		raw, err := os.ReadFile("/opt/sql/imserver.sql")
		must(err == nil, "cannot read /opt/sql/imserver.sql")
		n, bad := 0, 0
		var b strings.Builder
		for _, line := range strings.Split(string(raw), "\n") {
			s := strings.TrimSpace(line)
			if strings.HasPrefix(s, "--") {
				continue
			}
			b.WriteString(s)
			b.WriteByte(' ')
			if strings.HasSuffix(s, ";") {
				if st := strings.TrimSpace(b.String()); st != "" {
					n++
					if _, err := db.Exec(st); err != nil {
						bad++
						log.Printf("[jim-railway] schema statement failed: %v", err)
					}
				}
				b.Reset()
			}
		}
		must(bad == 0, fmt.Sprintf("schema load had %d failed statements of %d", bad, n))
		log.Printf("[jim-railway] schema loaded (%d statements)", n)
	} else {
		must(err == nil, "cannot inspect database: "+fmt.Sprint(err))
	}

	h := sha1.Sum([]byte(adminPass))
	_, err = db.Exec("INSERT INTO accounts (account, password, state, role_type) VALUES ('admin', ?, 0, 0) ON DUPLICATE KEY UPDATE password = VALUES(password), state = 0, role_type = 0", hex.EncodeToString(h[:]))
	must(err == nil, "cannot set the console admin password: "+fmt.Sprint(err))
	log.Printf("[jim-railway] console user 'admin' password set from JUGGLEIM_ADMIN_PASSWORD")

	connect := env("JUGGLEIM_CONNECT_ADDRESS", "")
	if connect == "" && os.Getenv("RAILWAY_PUBLIC_DOMAIN") != "" {
		connect = "wss://" + os.Getenv("RAILWAY_PUBLIC_DOMAIN")
	}
	if connect != "" {
		v, _ := json.Marshal(map[string][]string{"default": {connect}})
		_, err = db.Exec("INSERT INTO globalconfs (conf_key, conf_value) VALUES ('connect_address', ?) ON DUPLICATE KEY UPDATE conf_value = VALUES(conf_value)", string(v))
		must(err == nil, "cannot set connect_address: "+fmt.Sprint(err))
		log.Printf("[jim-railway] client SDKs will connect to %s", connect)
	}
}

func proxyTo(p int) *httputil.ReverseProxy {
	u, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", p))
	rp := httputil.NewSingleHostReverseProxy(u)
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "JuggleIM is starting", http.StatusBadGateway)
	}
	return rp
}

func serveProxy(port string) {
	ws, api, nav, admin := proxyTo(wsPort), proxyTo(apiPort), proxyTo(navPort), proxyTo(adminPort)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case p == "/health" || p == "/version" || p == "/im" || strings.HasPrefix(p, "/im/") || p == "/imbot":
			ws.ServeHTTP(w, r)
		case strings.HasPrefix(p, "/apigateway/"):
			api.ServeHTTP(w, r)
		case strings.HasPrefix(p, "/navigator/"):
			nav.ServeHTTP(w, r)
		case p == "/metrics" || p == "/jim" || strings.HasPrefix(p, "/jim/"):
			// Not exposed: unauthenticated metrics, and the bundled demo chat API
			// (its demo SMS/email login accepts a fixed code).
			http.NotFound(w, r)
		default:
			admin.ServeHTTP(w, r)
		}
	})
	srv := &http.Server{Addr: ":" + port, Handler: h, ReadHeaderTimeout: 30 * time.Second}
	log.Printf("[jim-railway] public listener on :%s", port)
	log.Fatal(srv.ListenAndServe())
}
