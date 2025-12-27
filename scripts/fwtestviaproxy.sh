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

if [[ "$1" == "" || "$HTTP_PROXY" == "" ]]; then
  log "Missing args.  Args are ip:port [...].  For IPv6, [ip]:port.  -d for debug.  Env var HTTP_PROXY required."
  exit 1
fi

. /etc/opensips/globalcfg.sh

[ "$DEBUG" == "Y" ] && DBG=1

[ $ENABLEOPENSIPS == Y ] && { log "OpenSIPS is enabled.  Please disable and try again."; exit 1; }

if pgrep -x opensips; then log "OpenSIPS is running.  Please stop it and try again."; exit 1; fi

[ $DBG ] && log "Configuring Nginx for test"

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

# get local/public IPs
. /scripts/getaddr.sh

GOODPROXY=N
# check if passed proxy works
if PROXYIP=$(curl -s -4 --max-time 10 --proxy $HTTP_PROXY http://icanhazip.com); then
  log "Proxy $HTTP_PROXY is using IP $PROXYIP"
  GOODPROXY=Y
else
  log "Proxy $HTTP_PROXY failed connecting to icanhazip.com.  Will try alts from pubproxy.com."
  for CNT in {0..10}; do
    [ $DBG ] && log "Try $CNT for proxy HTTP_PROXY via pubproxy.com."
    if PROXYJSON=$(curl -s http://pubproxy.com/api/proxy?country=US,CA); then
      if HTTP_PROXY=$(echo $PROXYJSON|jq -r .data[0].ipPort); then
        HTTP_PROXY=$(echo $PROXYJSON|jq -r .data[0].type)://$HTTP_PROXY
        log "HTTP_PROXY = $HTTP_PROXY"
        if PROXYIP=$(curl -s -4 --max-time 10 --proxy $HTTP_PROXY http://icanhazip.com); then
          log "Proxy $HTTP_PROXY is using IP $PROXYIP"
          GOODPROXY=Y
          break
        else
          log "Proxy $HTTP_PROXY failed connecting to icanhazip.com.  Will try again..."
        fi
      else
        log "Failed to get valid IP from pubproxy.com.  Will try again..."
        log "PROXYJSON=$PROXYJSON"
      fi
    else
      log "Failed connecting to pubproxy.com.  Will try again..."
    fi
    sleep 2
  done
fi

if [ "$GOODPROXY" == "N" ]; then
  log "Failed confirming working proxy.  Exiting."
  exit 1
fi

ALLOK=Y
while [ "$1" != "" ]; do
  [[ $1 =~ "-" ]] && { shift; continue; }
  log "Checking remote access to http://$1 via proxy $HTTP_PROXY."
  if RET=$(curl -k -s --proxy $HTTP_PROXY http://$1/testing/) && [[ "$RET" == "ok!" ]]; then
    log "Remote access success for http://$1 !!! Returned $RET"
  else
    ALLOK=N
    [[ $1 =~ "[" ]] && log "Failure for http://$1...  Check the firewall setting for the port." || log "Failure for http://$1...  Check IPv4 port forwarding setting in the router."
  fi
  log "Checking local access to http://$1"
  if RET=$(curl -k -s http://$1/testing/) && [[ "$RET" == "ok!" ]]; then
    log "Local access success for http://$1 !!! Returned $RET"
  else
    ALLOK=N
    log "Failure for http://$1...  Check NAT Loopback or Hairpin NAT settings in the router."
  fi
  shift
done

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
