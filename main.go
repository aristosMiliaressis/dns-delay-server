package main

import (
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	flag "github.com/spf13/pflag"

	"github.com/miekg/dns"
)

var choiceMap = make(map[string]int)

// adapted from https://gist.github.com/NinoM4ster/edaac29339371c6dde7cdb48776d2854 which was
// adapted from https://gist.github.com/walm/0d67b4fb2d5daf3edd4fad3e13b162cb

func newDNSHandler(records Records, aDelay, aaaaDelay time.Duration, authority string, alternateRecords bool) dns.HandlerFunc {
	return func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Compress = false

		if r.Opcode != dns.OpcodeQuery {
			log.Printf("Got a non-query message: %v\n", r)
			w.WriteMsg(m)
			return
		}

		for _, q := range m.Question {
			queryType := ""
			answers := []string{}
			cname := false
			delay := time.Duration(0)

			if strings.HasPrefix(q.Name, "cname.") {
				cname = true
				d := strings.TrimPrefix(q.Name, "cname.")
				log.Printf("Query for %s, replying with CNAME %s\n", q.Name, d)
				rr, err := dns.NewRR(fmt.Sprintf("%s CNAME %s", q.Name, d))
				if err == nil {
					m.Answer = append(m.Answer, rr)
				}
			}
			switch q.Qtype {
			case dns.TypeA:
				if cname && len(records.CNAMEA) > 0 {
					answers = records.CNAMEA
				} else {
					answers = records.A
				}
				queryType = "A"
				delay = aDelay

			case dns.TypeAAAA:
				if cname && len(records.CNAMEAAAA) > 0 {
					answers = records.CNAMEAAAA
				} else {
					answers = records.AAAA
				}
				queryType = "AAAA"
				delay = aaaaDelay
			}

			log.Printf("%s Query for %s, replying after %v\n", queryType, q.Name, delay)
			time.Sleep(delay)
			if len(answers) > 0 {
				d := q.Name
				if cname {
					d = strings.TrimPrefix(d, "cname.")
				}
				for _, ip := range answers {
					// Check if IP is valid
					if net.ParseIP(ip) == nil {
						continue
					}
					rr, err := dns.NewRR(fmt.Sprintf("%s %s %s", d, queryType, ip))
					if err != nil {
						log.Printf("Failed to create RR: %s\n", err.Error())
						continue
					}
					m.Answer = append(m.Answer, rr)
				}

				if alternateRecords {
					if _, ok := choiceMap[q.Name]; !ok {
						choiceMap[q.Name] = 0
					}
					choiceMap[q.Name]++
					m.Answer = []dns.RR{m.Answer[choiceMap[q.Name]%2]}
				}
			}
		}

		if len(authority) > 0 {
			rr, err := dns.NewRR(fmt.Sprintf("%s NS %s", m.Question[0].Name, authority))
			if err == nil {
				m.Ns = append(m.Ns, rr)
			}
		}
		m.Authoritative = true

		for _, answer := range m.Answer {
			log.Printf("resonding with: %s", answer.String())
		}

		w.WriteMsg(m)
	}
}

type Records struct {
	A         []string
	AAAA      []string
	CNAMEA    []string
	CNAMEAAAA []string
}

func main() {
	// Flags
	port := flag.IntP("port", "p", 5353, "port to listen on")
	listenAddr := flag.StringP("listen", "l", "0.0.0.0", "address to listen on")
	aRecords := flag.StringSliceP("a", "a", []string{}, "A records to serve")
	aaaaRecords := flag.StringSliceP("aaaa", "6", []string{}, "AAAA records to serve")
	aDelay := flag.DurationP("delay-a", "d", 0, "delay before serving to A records - give an invalid IP address to prevent A records from being served with CNAMES")
	aaaaDelay := flag.DurationP("delay-aaaa", "D", 0, "delay before serving to AAAA records - give an invalid IP address to prevent AAAA records from being served with CNAMES")
	alternateRecords := flag.BoolP("alternate", "A", false, "Alternate records, useful for TOCTOU race conditions")
	aCname := flag.StringSliceP("cname-a", "c", []string{}, "A record to serve for CNAME queries")
	aaaaCname := flag.StringSliceP("cname-aaaa", "C", []string{}, "AAAA record to serve for CNAME queries")
	authority := flag.StringP("authority", "", "", "authority to serve")
	flag.Parse()

	records := Records{
		A:         *aRecords,
		AAAA:      *aaaaRecords,
		CNAMEA:    *aCname,
		CNAMEAAAA: *aaaaCname,
	}

	// attach request handler func
	dns.HandleFunc(".", newDNSHandler(records, *aDelay, *aaaaDelay, *authority, *alternateRecords))

	// start server
	server := &dns.Server{Addr: fmt.Sprintf("%s:%d", *listenAddr, *port), Net: "udp"}
	log.Printf("Starting at %s\n", server.Addr)

	err := server.ListenAndServe()
	if err != nil {
		log.Fatalf("Failed to start server: %s\n ", err.Error())
	}

	defer server.Shutdown()
}
