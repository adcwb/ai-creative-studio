#!/usr/bin/env python3
"""Bounded unauthenticated health-load probe of an explicitly local server only."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import ipaddress
import json
import statistics
import time
import urllib.error
import urllib.parse
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base-url', default='http://127.0.0.1:8080')
    parser.add_argument('--requests', type=int, default=100)
    parser.add_argument('--concurrency', type=int, default=4)
    args = parser.parse_args()
    url = urllib.parse.urlsplit(args.base_url)
    try:
        local = ipaddress.ip_address(url.hostname).is_loopback
    except ValueError:
        local = False
    if not local or url.scheme != 'http' or url.username or url.password or url.query or url.fragment or url.path not in ['', '/']:
        parser.error('only explicit loopback HTTP origins are allowed')
    if not 1 <= args.requests <= 10000 or not 1 <= args.concurrency <= 32:
        parser.error('requests must be 1..10000 and concurrency 1..32')
    # Disable proxies and redirects; no credentials are read or transmitted.
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *unused, **kwargs):
            return None
    def probe(unused):
        start = time.monotonic()
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
        try:
            with opener.open(args.base_url.rstrip('/') + '/healthz', timeout=10) as response:
                ok = response.status == 200 and len(response.read(4097)) <= 4096
        except (OSError, urllib.error.URLError):
            ok = False
        return ok, (time.monotonic() - start) * 1000
    start = time.monotonic()
    with ThreadPoolExecutor(max_workers=args.concurrency) as pool:
        results = list(pool.map(probe, range(args.requests)))
    times = sorted(value[1] for value in results)
    failed = sum(not value[0] for value in results)
    print(json.dumps({'scope': 'local health endpoint only; not creative worker capacity', 'requests': len(results), 'concurrency': args.concurrency, 'failed': failed, 'elapsed_seconds': time.monotonic() - start, 'p50_ms': statistics.median(times), 'p95_ms': times[max(0, int(len(times) * .95) - 1)]}, indent=2))
    raise SystemExit(1 if failed else 0)

if __name__ == '__main__':
    main()
