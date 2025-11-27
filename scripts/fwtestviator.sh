#!/bin/bash -eE
# check router's firewall and port forward settings.  root needed.
# args are ip:port [...]
# if IPv6, use [ip]:port
# -d is debug

function __error_handing__(){
    local last_status_code=$1;
    local error_line_number=$2;
    echo 1>&2 "Error - exited with status $last_status_code at line $error_line_number";
    perl -slne 'if($.+5 >= $ln && $.-4 <= $ln){ $_="$. $_"; s/$ln/">" x length($ln)/eg; s/^\D+.*?$/\e[1;31m$&\e[0m/g;  print}' -- -ln=$error_line_number $0
}
trap  '__error_handing__ $? $LINENO' ERR

[[ $@ =~ "-d" ]] && DBG=1

SCR=$(basename $0)
log() {
  echo $(date "+%m-%d %H:%M:%S") [$$] - $1
  echo "$1" | logger -p local6.1 -t $SCR
}

if [ "$1" == "" ]; then
  log "Missing args.  Args are ip:port [...].  For IPv6, [ip]:port.  -d for debug.  "
  exit 1
fi

. /etc/opensips/globalcfg.sh

[ "$DEBUG" == "Y" ] && DBG=1

[ $ENABLEOPENSIPS == Y ] && { log "OpenSIPS is enabled.  Please disable and try again."; exit 1; }

if pgrep -x opensips; then log "OpenSIPS is running.  Please stop it and try again."; exit 1; fi

[ $DBG ] || TOROPT=--quiet

[ $DBG ] && log "Configuring Nginx for test"

#cp $LOCUSERDIR/$LOCUSERCERT $LOCUSERDIR/fullchain-$LOCUSERCERT
#cat $LOCUSERDIR/$LOCUSERCALIST | tee -a $LOCUSERDIR/fullchain-$LOCUSERCERT > /dev/null

#NGINXSITES=/etc/opensips/nginx/sites-enabled
NGINXSITES=/etc/opensips/nginx/sites-available
NGINXSITESENABLED=/etc/opensips/nginx/sites-enabled

# cleanup sites
rm $NGINXSITESENABLED/*
ln -fs $NGINXSITES/admin $NGINXSITESENABLED/admin

unset LISTENWEB
[ $IPADDRSUP != IPV4 ] || LISTENWEB="
    listen [::]:38443 default_server ipv6only=on;
    listen [::]:5061 default_server ipv6only=on;
"
# need test config site
cat > $NGINXSITES/testing  <<EOF
server {
    listen 38443 default_server;
    listen 5061 default_server;
$LISTENWEB
    location /testing/ {
                root   /var/www/html;
                index  index.html index.htm ok.txt;
    }
}
EOF
ln -fs $NGINXSITES/testing $NGINXSITESENABLED/testing

[ $DBG ] && log "Added Nhinx config: $(cat $NGINXSITES/testing)"

mkdir -p /var/www/html/testing
echo ok! > /var/www/html/testing/ok.txt

if RET=$(nginx -t -c /etc/opensips/nginx/nginx.conf 2>&1); then
  kill -HUP $(cat /run/nginx.pid)
  [ $DBG ] && log "Nginx config reloaded"
else
  log "Error in Nginx config: $RET"
  exit 1
fi

# need tor

[ $DBG ] && log "Starting tor..."
runuser -u mmsgate -- tor $TOROPT &
TORPID=$!

# get local/public IPs
. /scripts/getaddr.sh

# wait for tor to be up
TOR=DOWN
sleep 3
for i in {0..10}; do
  [ $DBG ] && log "Checking tor up?"
  TORRMTIP=$(curl -s --socks5-hostname 127.0.0.1:9050 https://icanhazip.com) && TOR=UP
  [ $DBG ] && log "Tor is $TOR"
  [ $TOR == UP ] && break
  sleep 3
done

if [ $TOR != UP ]; then
  log "Tor did not come up in time.  Exiting..."
  kill -TERM $TORPID
  exit 1
fi

[ $DBG ] && log "Tor started and using IP $TORRMTIP"

ALLOK=Y
while [ "$1" != "" ]; do
  [[ $1 =~ "-" ]] && { shift; continue; }
  log "Checking remote access to http://$1"
  if RET=$(curl -k -s --socks5-hostname 127.0.0.1:9050 http://$1/testing/); then
    log "Remote access success for http://$1 !!!"
  else
    ALLOK=N
    [[ $1 =~ "[" ]] && log "Failure for http://$1...  Check the firewall setting for the port." || log "Failure for http://$1...  Check forwarding to local $LOCIPV4 setting for the port."
  fi
  log "Checking local access to http://$1"
  if RET=$(curl -k -s http://$1/testing/); then
    log "Local access success for http://$1 !!!"
  else
    ALLOK=N
    log "Failure for http://$1...  Check NAT Loopback or Hairpin NAT settings."
  fi
  shift
done

[ $DBG ] && log "Stopping tor"
#kill $TORPID
if pkill -x tor; then log "Tor stopped"; else log "Tor could not be stopped."; fi

[ $DBG ] && log "Cleaning up Nginx config"
rm $NGINXSITESENABLED/testing
[ "$DNSNAME" != "" ] && ln -fs $NGINXSITES/$DNSNAME $NGINXSITESENABLED/$DNSNAME
kill -HUP $(cat /run/nginx.pid)

if [ $ALLOK == N ]; then
  log "Firewall check failed..."
  exit 1
else
  log "Congratulations! The firewall is configured correctly"
  exit 0
fi
