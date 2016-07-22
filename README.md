The dnscache-stats program can be used to send metrics generated from
the log entries of the [dnscache][1] program to a graphite-compatible
metrics database.

# Usage

	dnscache-stats [-i 60s] [-t metrics-template] [-f file] graphite-host[:port]

Dnscache-stats will read data on standard input, and upload various
metrics to the provided server using Graphite's [plain-text protocol
format][2]. If graphite-host is a file name (begins with '.' or '/'),
metrics are appended to the file instead. All data read on stdin is
echoed to stdout, unmodified, so that dnscache-stats can be used as a
log script for dnscache:

	#!/bin/sh
	exec setuidgid Gdnslog graphite-stats graphite.example.net | \
		exec setuidgid Gdnslog multilog t ./main

This is the recommended way to run dnscache-stats. Alternatively, it can be
run as a standalone service using the -f flag. Here is an example systemd
unit file for such a service:

	[Unit]
	Description=send dnscache metrics to graphite
	
	[Service]
	ExecStart=/usr/bin/dnscache-stats -f /service/dnscache/log/main/current graphite.example.net
	Restart=always
	
	[Install]
	WantedBy=multi-user.target

By default, dnscache-stats will generate one data point for each metric every minute.
The interval between data points can be changed with the -i option. To change the
interval to 5 minutes, run:

	dnscache-stats -i 5m graphite.example.net

By default, metrics names will be of the form

	servers.HOSTNAME.dnscache.METRIC_NAME

This naming convention is taken from [Diamond][3]. The -t flag can be used to change
the naming convention. For instance, to change the naming convention to the collectd
convention, run:

	dnscache-stats -t 'collectd.{{.Hostname}}.dnscache.{{.Metric}}'

Within the template, `.Service` will be the name of the service directory used by
daemontools, if it is possible to infer. For instance, if dnscache-stats is run from
`/service/dnscache-primary/log`, then the command

	dnscache-stats -t servers.{{.Hostname}}.dnscache.{{.Service|rstrip "dnscache-"}}.{{.Metric}}

will generate metrics such as the following

	servers.myhost01.dnscache.primary.cache_motion

# Metrics collected

dnscache-stats collects the following metrics. The following description
assumes the default interval (-i option) of 1 minute:

- `queries`: number of queries received in the last minute
- `cache_motion`: number of bytes written to the cache in the last minute
- `udp_active`: max sample of the number of pending (unfinished) UDP queries over the last minute
- `tcp_active`: max sample of the number of pending (unfinished) TCP queries over the last minute
- `cache_hits`: number of cache hits
- `drop`: the number of dropped queries
- `query_avg`: mean query time of all requests in the last minute
- `query_max`: slowest query in the last minute
- `servfail`: number of failure responses sent
- `tx`: number of outgoing queries dnscache made to resolve a record

See [this page][4] for a helpful description of the information available in
dnscache's log output.

[1]: https://cr.yp.to/djbdns/dnscache.html
[2]: http://graphite.readthedocs.io/en/latest/feeding-carbon.html#the-plaintext-protocol
[3]: https://github.com/BrightcoveOS/Diamond
[4]: http://www.dqd.com/~mayoff/notes/djbdns/dnscache-log.html

# Operational notes

- When measuring the `query_avg` and `query_max` metrics, dnscache-stats
  will only consider the first 100,000 queries. This is to prevent dnscache-stats
  from consuming an unpredictable amount of memory and is controlled by
  the compile-time constant `QueryTrackingLimit`.
- dnscache-stats will buffer up to 100 entries of each metric in memory if they
  cannot be sent to the graphite server. Once the buffer is filled, the oldest metrics
  are dropped first.
- dnscache-stats will exit if it can no longer write to stdout. This is important when
  running dnscache-stats as a log program under daemontools, as it will ensure that
  the supervise program will restart it.
- When following a physical file (via the -f flag), dnscache will attempt to detect and
  re-open the file if it is renamed (such as during log rotation). This process is
  imperfect and can result in (very small) errors in metrics.

# Building and developing dnscache-stats

Dnscache-stats is written with Go, and can be built with Go version 1.4 or above.
To build dnscache, run

	go build

Or, to fetch, build and install dnscache-stats to $GOPATH/bin, run

	go get github.com/droyo/dnscache-stats

Pull requests are welcome, and any issues can be opened through github.
Please run `go test` before submitting any changes.

# Packaging and deploying dnscache-stats

The `contrib` directory contains an RPM spec file that can be used to package
the dnscache-stats program. Pre-packaged RPMs and binary tarballs can
be found under the github releases for this repository. Binary releases are
built with the latest stable version of Go at the time of the release.
