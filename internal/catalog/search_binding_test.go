package catalog_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rustambekabdusalomov/shop-api/internal/catalog"
)

// wireEscapeReplacer mirrors the escaping contract Search must apply: the
// LIKE wildcards and the escape character itself match literally. It is
// written out here (instead of reusing the implementation) so this test
// compiles against the code before the fix too.
var wireEscapeReplacer = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// wireCapture records what the server side sees, guarded for the server
// goroutine and the test goroutine.
type wireCapture struct {
	mu    sync.Mutex
	stmts []string
	binds [][]string
	err   string
}

func (c *wireCapture) recordStmt(sql string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stmts = append(c.stmts, sql)
}

func (c *wireCapture) recordBind(params []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.binds = append(c.binds, params)
}

func (c *wireCapture) bindCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.binds)
}

func (c *wireCapture) bindAt(i int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.binds[i]...)
}

func (c *wireCapture) statements() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.stmts...)
}

func (c *wireCapture) noteErr(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == "" {
		c.err = s
	}
}

func (c *wireCapture) serverErr() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// newWirePool returns a pool whose connections are served by serveWire on an
// in-memory pipe instead of a real database.
func newWirePool(t *testing.T) (*pgxpool.Pool, *wireCapture) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://wire:wire@localhost:1/wire?sslmode=disable")
	if err != nil {
		t.Fatalf("parse pool config: %v", err)
	}
	cfg.MaxConns = 1
	cap := &wireCapture{}
	cfg.ConnConfig.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
		client, server := net.Pipe()
		go serveWire(server, cap)
		return client, nil
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	return pool, cap
}

// TestSearchBindsUserInputAsParameter proves the injection is closed without
// needing a database: the stand-in server captures the statement SQL and the
// bound values Search emits. The SQL text must be byte-identical for hostile
// and benign inputs alike, with the term travelling only as a bound argument.
func TestSearchBindsUserInputAsParameter(t *testing.T) {
	inputs := []string{
		"aurora",
		"' OR '1'='1",
		"' UNION SELECT id, sku, name, description, price_cents, image_url, stock, created_at FROM shop.products -- ",
		"%",
		"_",
		`\`,
	}
	pool, cap := newWirePool(t)
	defer pool.Close()
	repo := catalog.NewRepo(pool)

	for _, q := range inputs {
		t.Run(q, func(t *testing.T) {
			before := cap.bindCount()
			products, err := repo.Search(context.Background(), q, 7, 20)
			if err != nil {
				t.Fatalf("Search(%q) returned error: %v", q, err)
			}
			if len(products) != 0 {
				t.Fatalf("Search(%q) returned %d products, want 0", q, len(products))
			}
			if err := cap.serverErr(); err != "" {
				t.Fatalf("wire server error during Search(%q): %s", q, err)
			}
			if got := cap.bindCount() - before; got != 1 {
				t.Fatalf("Search(%q) sent %d Bind messages, want 1", q, got)
			}
			got := cap.bindAt(before)
			if len(got) != 3 {
				t.Fatalf("Search(%q) bound %d params %q, want 3 (term, after, limit)", q, len(got), got)
			}
			if want := wireEscapeReplacer.Replace(q); got[0] != want {
				t.Errorf("Search(%q) bound term %q, want escaped %q", q, got[0], want)
			}
			if got[1] != "7" || got[2] != "20" {
				t.Errorf("Search(%q) bound after/limit %q, want [7 20]", q, got[1:])
			}
		})
	}

	stmts := cap.statements()
	if len(stmts) == 0 {
		t.Fatal("wire server captured no statements")
	}
	for _, s := range stmts[1:] {
		if s != stmts[0] {
			t.Errorf("user input reached the SQL text:\nfirst: %s\nlater: %s", stmts[0], s)
		}
	}
}

// serveWire speaks just enough of the PostgreSQL wire protocol for pgx to
// prepare and run queries: startup, Parse (captures the statement SQL),
// Describe (declares text params from the statement's $N placeholders, no
// result columns), Bind (captures the bound values), Execute (zero rows).
func serveWire(conn net.Conn, cap *wireCapture) {
	defer conn.Close()

	readN := func(n int) ([]byte, error) {
		b := make([]byte, n)
		_, err := io.ReadFull(conn, b)
		return b, err
	}
	readInt32 := func() (int32, error) {
		b, err := readN(4)
		if err != nil {
			return 0, err
		}
		return int32(binary.BigEndian.Uint32(b)), nil
	}
	writeMsg := func(typ byte, payload []byte) error {
		msg := make([]byte, 0, 5+len(payload))
		if typ != 0 {
			msg = append(msg, typ)
		}
		var ln [4]byte
		binary.BigEndian.PutUint32(ln[:], uint32(4+len(payload)))
		msg = append(msg, ln[:]...)
		msg = append(msg, payload...)
		_, err := conn.Write(msg)
		return err
	}
	readCStrings := func(b []byte, n int) ([]string, error) {
		out := make([]string, 0, n)
		for range n {
			i := 0
			for i < len(b) && b[i] != 0 {
				i++
			}
			if i >= len(b) {
				return nil, fmt.Errorf("short cstring")
			}
			out = append(out, string(b[:i]))
			b = b[i+1:]
		}
		return out, nil
	}
	fail := func(err error) {
		cap.noteErr(err.Error())
	}

	// Startup (no message-type byte). Answer an SSL request if one arrives,
	// then complete the handshake.
	for {
		ln, err := readInt32()
		if err != nil {
			cap.noteErr("startup: " + err.Error())
			return
		}
		proto, err := readInt32()
		if err != nil {
			cap.noteErr("startup: " + err.Error())
			return
		}
		if _, err := readN(int(ln) - 8); err != nil {
			cap.noteErr("startup: " + err.Error())
			return
		}
		if proto == 80877103 {
			if _, err := conn.Write([]byte{'N'}); err != nil {
				cap.noteErr("startup: " + err.Error())
				return
			}
			continue
		}
		break
	}
	authOK := func() error { return writeMsg('R', []byte{0, 0, 0, 0}) }
	param := func(name, value string) error {
		return writeMsg('S', append(append([]byte(name), 0), append([]byte(value), 0)...))
	}
	if err := authOK(); err != nil {
		return
	}
	for _, kv := range [][2]string{
		{"server_version", "16.0"},
		{"server_encoding", "UTF8"},
		{"client_encoding", "UTF8"},
		{"DateStyle", "ISO, MDY"},
		{"TimeZone", "UTC"},
		{"integer_datetimes", "on"},
		{"standard_conforming_strings", "on"},
	} {
		if err := param(kv[0], kv[1]); err != nil {
			return
		}
	}
	var keydata [8]byte
	if err := writeMsg('K', keydata[:]); err != nil {
		return
	}
	if err := writeMsg('Z', []byte{'I'}); err != nil {
		return
	}

	parsed := map[string]string{}
	for {
		typ := []byte{0}
		if _, err := io.ReadFull(conn, typ); err != nil {
			return // closed, e.g. pool.Close
		}
		ln, err := readInt32()
		if err != nil {
			cap.noteErr("frame: " + err.Error())
			return
		}
		payload, err := readN(int(ln) - 4)
		if err != nil {
			cap.noteErr("frame: " + err.Error())
			return
		}
		switch typ[0] {
		case 'P': // Parse: name\0 sql\0 int16 count {int32 oid}
			names, err := readCStrings(payload, 2)
			if err != nil {
				fail(fmt.Errorf("Parse: %w", err))
				return
			}
			parsed[names[0]] = names[1]
			// Record only catalog statements so unrelated chatter, if
			// any, cannot affect the assertions.
			if strings.Contains(names[1], "shop.products") {
				cap.recordStmt(names[1])
			}
			if err := writeMsg('1', nil); err != nil {
				return
			}
		case 'D': // Describe: 'S'|'P' name\0
			if len(payload) < 2 || (payload[0] != 'S' && payload[0] != 'P') {
				fail(fmt.Errorf("bad Describe"))
				return
			}
			if payload[0] == 'S' {
				name := string(payload[1 : len(payload)-1])
				n := countPlaceholders(parsed[name])
				oids := make([]byte, 4+4*n)
				binary.BigEndian.PutUint32(oids[:4], uint32(n))
				for i := range n {
					binary.BigEndian.PutUint32(oids[4+4*i:4+4*i+4], 25) // text
				}
				if err := writeMsg('t', oids); err != nil {
					return
				}
			} else if err := writeMsg('n', nil); err != nil { // NoData: zero rows
				return
			}
		case 'S': // Sync
			if err := writeMsg('Z', []byte{'I'}); err != nil {
				return
			}
		case 'B': // Bind: portal\0 stmt\0 formats params result-formats
			rest := payload
			take := func() string {
				i := 0
				for rest[i] != 0 {
					i++
				}
				s := string(rest[:i])
				rest = rest[i+1:]
				return s
			}
			_ = take() // portal
			_ = take() // statement
			var vals []string
			if len(rest) >= 2 {
				fc := int(binary.BigEndian.Uint16(rest[:2]))
				rest = rest[2+2*fc:]
			}
			if len(rest) >= 2 {
				pc := int(binary.BigEndian.Uint16(rest[:2]))
				rest = rest[2:]
				for range pc {
					if len(rest) < 4 {
						fail(fmt.Errorf("short Bind param"))
						return
					}
					ln := int32(binary.BigEndian.Uint32(rest[:4]))
					rest = rest[4:]
					if ln < 0 {
						vals = append(vals, "")
						continue
					}
					if len(rest) < int(ln) {
						fail(fmt.Errorf("short Bind value"))
						return
					}
					vals = append(vals, string(rest[:ln]))
					rest = rest[ln:]
				}
			}
			cap.recordBind(vals)
			if err := writeMsg('2', nil); err != nil { // BindComplete
				return
			}
		case 'E': // Execute: answer zero rows.
			if err := writeMsg('C', append([]byte("SELECT 0"), 0)); err != nil {
				return
			}
		case 'H': // Flush: nothing to send.
		case 'Q': // Simple query: not used by Search; answer empty-handed.
			if err := writeMsg('C', append([]byte("SELECT 0"), 0)); err != nil {
				return
			}
			if err := writeMsg('Z', []byte{'I'}); err != nil {
				return
			}
		case 'X': // Terminate.
			return
		default:
			fail(fmt.Errorf("unexpected message %q", typ[0]))
			return
		}
	}
}

// countPlaceholders returns the highest $N placeholder in sql (0 if none).
func countPlaceholders(sql string) int {
	max := 0
	for i := 0; i+1 < len(sql); i++ {
		if sql[i] != '$' || sql[i+1] < '0' || sql[i+1] > '9' {
			continue
		}
		j := i + 1
		n := 0
		for j < len(sql) && sql[j] >= '0' && sql[j] <= '9' {
			n = n*10 + int(sql[j]-'0')
			j++
		}
		if n > max {
			max = n
		}
	}
	return max
}
