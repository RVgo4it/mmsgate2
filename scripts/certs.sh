#!/bin/bash

# this script is will get a certificate from Let's Encrypt via LEGO.  It also sets it up in Nginx.  Optional subcommand for LEGO must be first.  Must run as root.

SCR=$(basename $0)
log() {
  echo $(date "+%m-%d %H:%M:%S") [$$] $SCR - "$1"
  echo "$1" | logger -p local6.1 -t $SCR
}

source /etc/opensips/globalcfg.sh
[ "$DEBUG" == "Y" ] && DBG=1

# -s option points to staging server
unset LEGOOPT
[[ $@ =~ "-s" ]] && LEGOOPT=--server=https://acme-staging-v02.api.letsencrypt.org/directory

[ $DBG ] && LEGOOPT="--log.level debug $LEGOOPT"
[ $DBG ] && log "Certs starting!"
[ $DBG ] && log "Args: $@"

# Some global setting are required
[ "$DNSNAME" == "" ] && { [ $DBG ] && log "Missing global DNSNAME"; exit 1; }
[ "$EMAIL" == "" ] && { [ $DBG ] && log "Missing required email address"; exit 1; }

[ $DBG ] && log "DNS name is $DNSNAME"

# some fixed paths
NGINXSITES=/etc/opensips/nginx/sites-available
NGINXSITESENABLED=/etc/opensips/nginx/sites-enabled
CERTS=/etc/opensips/tls

# clean up any extra sites...
for SITE in $(ls $NGINXSITES); do
  if [ $SITE != $DNSNAME ] && [ $SITE != testing ] && [ $SITE != admin ] && [ $SITE != default ]; then
    [ $DBG ] && log "Removing $NGINXSITES/$SITE"
    rm -r $NGINXSITES/$SITE
  fi
done
for SITE in $(ls $NGINXSITESENABLED); do
  if [ $SITE != $DNSNAME ] && [ $SITE != admin ]; then
    [ $DBG ] && log "Removing $NGINXSITESENABLED/$SITE"
    rm -r $NGINXSITESENABLED/$SITE
  fi
done

# if not already done, create Nginx config
[ -e $NGINXSITES/$DNSNAME ] || cat > $NGINXSITES/$DNSNAME <<EOF
server {
    listen              38443 ssl;
    listen              [::]:38443 ssl;
    client_max_body_size 1G;
    server_name         $DNSNAME;
    ssl_protocols       TLSv1 TLSv1.1 TLSv1.2 TLSv1.3;
    ssl_ciphers         HIGH:!aNULL:!MD5;
    ssl_certificate     $CERTS/certificates/$DNSNAME.crt;
    ssl_certificate_key $CERTS/certificates/$DNSNAME.key;
    location / {
        proxy_pass http://127.0.0.1:38080;
        include proxy_params;
    }
    location /admin {
        deny  all;
    }
    location /mmsmedia/ {
                alias  /data/mmsmedia/;
                index  index.html;
    }
    location /testing/ {
                root   /var/www/html;
                index  index.html index.htm ok.txt;
    }
}
EOF
# and enable it
ln -fs $NGINXSITES/$DNSNAME $NGINXSITESENABLED/$DNSNAME

# need migrate v4 -> v5?
if [ -e $CERTS/accounts/*/*/keys ]; then
  log "Running lego migrate"
  R=$(echo Y | lego migrate --path $CERTS 2>&1)
  RET=$?
  [ $DBG ] && log "lego returned $RET"
  # log results
  RL=$(echo;echo "$R")
  log "$RL"
fi

# create or renew certs
[ $DBG ] && log "Running lego"

# get lego env
lget() { local NAME; local VAL; unset IFS; while read -r -d = NAME; read -r VAL; do eval export $NAME="$VAL"; done </etc/opensips/custdns.txt; }
lget

# domian to use for cert and challenge
if [[ "$DNSNAME" == "$DNSCERTDOM" || "$DNSCERTDOM" == "" ]]; then
  LEGOOPT="-d $DNSNAME $LEGOOPT"
else
  LEGOOPT="-d *.$DNSCERTDOM -d $DNSCERTDOM $LEGOOPT"
fi

# need to call /scripts/certdeploy.sh after cert deploy
LEGOOPT="--deploy-hook /scripts/certdeploy.sh $LEGOOPT"

# more options
LEGOOPT="--accept-tos --email $EMAIL --path $CERTS --pem --dns $DNSTYPE $LEGOOPT"

[ $DBG ] && log "LEGOOPT=$LEGOOPT"
R=$(lego run $LEGOOPT 2>&1)
RET=$?
[ $DBG ] && log "lego returned $RET"
# log results
RL=$(echo;echo "$R")
log "$RL"

exit $RET
