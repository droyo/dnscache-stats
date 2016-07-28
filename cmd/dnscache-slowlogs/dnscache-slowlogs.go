// The dnscache-slowlogs command can isolate and
// identify slow transactions in a dnscache log file.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"os"
	"strings"
	"time"

	"github.com/paulhammond/tai64"
)

var (
	interval = flag.Duration("i", time.Second*15, "threshold for slow queries")
)

type requestKey struct {
	name, ip string
}

type requestInfo struct {
	when    time.Time
	servers []string
	order   int
}

func slowlogs(input io.Reader, slowThreshold time.Duration) {
	scanner := bufio.NewScanner(input)
	requests := make(map[requestKey]requestInfo)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		now, err := tai64.ParseTai64n(fields[0])
		if err != nil {
			continue
		}
		switch fields[1] {
		case "tx":
			// glue typeno name control serverip ...
			// dnscache tries 1 serverip at a time,
			// left to right, and tries the whole list
			// 4 times.
			if len(fields) < 6 {
				println("short", fields[1])
				break
			}
			servers := fields[6:]
			for i, ip := range servers {
				key := requestKey{
					name: fields[4],
					ip:   ip,
				}
				requests[key] = requestInfo{
					when:    now,
					servers: servers,
					order:   i,
				}
			}
		case "rr":
			// serverip ttl type name data ...
			if len(fields) < 6 {
				println("short", fields[1])
				break
			}
			key := requestKey{
				name: fields[5],
				ip:   fields[2],
			}
			req, ok := requests[key]
			if !ok {
				break
			}
			for i := 0; i < req.order; i++ {
				fmt.Println("txfail", parseIP(req.servers[i]), key.name)
			}
			d := now.Sub(req.when)
			if d > slowThreshold {
				fmt.Println("txslow", parseIP(key.ip), key.name, d)
			}
			for _, ip := range req.servers {
				delete(requests, requestKey{name: key.name, ip: ip})
			}
		}
	}
	if scanner.Err() != nil {
		log.Fatal(scanner.Err())
	}
}

func parseIP(hex string) string {
	var x big.Int
	if _, ok := x.SetString(hex, 16); !ok {
		return hex
	}
	return net.IP(x.Bytes()).String()
}

func main() {
	var input io.Reader

	flag.Parse()
	if flag.NArg() > 0 {
		file, err := os.Open(flag.Arg(0))
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()
		input = file
	} else {
		input = os.Stdin
	}

	slowlogs(input, *interval)
}
