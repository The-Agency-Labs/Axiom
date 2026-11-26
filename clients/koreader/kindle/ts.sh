#!/bin/sh
# Start/stop Tailscale on the Kindle (used by the Axiom KOReader plugin).
# Everything lives in /mnt/us/tailscale; nothing touches the system partition.
DIR=/mnt/us/tailscale
SOCK=/var/tmp/tailscaled.sock
PORT=41641
TS="$DIR/tailscale --socket=$SOCK"

rules() {  # $1 = -I (add) or -D (delete)
    iptables "$1" INPUT -i tailscale0 -m state --state RELATED,ESTABLISHED -j ACCEPT 2>/dev/null
    iptables "$1" INPUT -i wlan0 -p udp --dport $PORT -j ACCEPT 2>/dev/null
    iptables "$1" INPUT -i wlan0 -p udp -m state --state RELATED,ESTABLISHED -j ACCEPT 2>/dev/null
}

case "$1" in
start)
    if ! pidof tailscaled >/dev/null; then
        rules -D; rules -I
        "$DIR/tailscaled" --statedir="$DIR/state" --socket=$SOCK --tun=tailscale0 \
            --port=$PORT >"$DIR/tailscaled.log" 2>&1 &
    fi
    for _ in $(seq 1 20); do
        state=$($TS status --json 2>/dev/null | grep -m1 '"BackendState"' | cut -d'"' -f4)
        case "$state" in
        Running) exit 0 ;;
        Stopped) $TS up --hostname=kindle --accept-dns=false --netfilter-mode=off \
                     --timeout=15s >/dev/null 2>&1 ;;
        esac
        sleep 1
    done
    exit 1
    ;;
stop)
    # Only end the daemon: "tailscale down" would keep it offline next start.
    killall tailscaled 2>/dev/null
    rules -D
    ;;
status)
    $TS status
    ;;
*)
    echo "usage: $0 start|stop|status"; exit 2
    ;;
esac
