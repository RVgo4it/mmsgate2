#!/bin/bash

# this script is will get a certificate from Let's Encrypt via LEGO.  It also sets it up in Nginx.  Optional subcommand for LEGO must be first.  Must run as root.

[[ "$1" =~ ^- ]] || LEGOCMD=$1

[[ $@ =~ "-d" ]] && DBG=1

[[ $@ =~ "-o" ]] && NOOPENSIPS=1

[[ $@ =~ "-s" ]] && LEGOOPT=--server=https://acme-staging-v02.api.letsencrypt.org/directory

SCR=$(basename $0)
log() {
  echo $(date "+%m-%d %H:%M:%S") [$$] $SCR - "$1"
  echo "$1" | logger -p local6.1 -t $SCR
}

source /etc/opensips/globalcfg.sh
[ "$DEBUG" == "Y" ] && DBG=1

[ $DBG ] && log "Certs starting!"
[ $DBG ] && log "Args: $*"
[ $CERTBOTCMD ] && [ $DBG ] && log "Will use lego $CERTBOTCMD"

# Some global setting are required
[ "$DNSNAME" == "" ] && { [ $DBG ] && log "Missing global DNSNAME"; exit 1; }
[ "$EMAIL" == "" ] && { [ $DBG ] && log "Missing required email address"; exit 1; }
[ "$DNSTOKEN" == "" ] && { [ $DBG ] && log "Missing required DNSTOKEN key"; exit 1; }

[ $DBG ] && log "DNS name is $DNSNAME"

NGINXSITES=/etc/opensips/nginx/sites-available
NGINXSITESENABLED=/etc/opensips/nginx/sites-enabled
CERTS=/etc/opensips/tls

# clean up any extra sites...
for SITE in $(ls $NGINXSITES); do
#  if [ $SITE != $DNSNAME ] && [ $SITE != testing ]; then
  if [ $SITE != $DNSNAME ] && [ $SITE != testing ] && [ $SITE != admin ] && [ $SITE != default ]; then
    [ $DBG ] && log "Removing $NGINXSITES/$SITE"
    rm -r $NGINXSITES/$SITE
  fi
done
for SITE in $(ls $NGINXSITESENABLED); do
#  if [ $SITE != $DNSNAME ] && [ $SITE != testing ]; then
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

# create or renew certs
[ $DBG ] && log "Running lego"

# no --run-hook=/scripts/certdeploy.sh option in this ver, so we'll simulate it...
PRECERTTS=$(stat -c %y $CERTS/certificates/$DNSNAME.crt 2>&1)
[ $DBG ] && log "Pre lego: $PRECERTTS"

if [ "$LEGOCMD" != "" ] ; then
  R=$(DYNU_API_KEY=$DNSTOKEN \
    lego --accept-tos --email $EMAIL --path $CERTS --pem --dns dynu -d $DNSNAME $LEGOOPT $LEGOCMD 2>&1)
else
  if [ -e $CERTS/certificates/$DNSNAME.key ] ; then
    R=$(DYNU_API_KEY=$DNSTOKEN \
      lego --accept-tos --email $EMAIL --path $CERTS --pem --dns dynu -d $DNSNAME $LEGOOPT renew 2>&1)
  else
    R=$(DYNU_API_KEY=$DNSTOKEN \
      lego --accept-tos --email $EMAIL --path $CERTS --pem --dns dynu -d $DNSNAME $LEGOOPT run 2>&1)
  fi
fi
RET=$?
[ $DBG ] && log "lego returned $RET"
# log results
RL=$(echo "$R"|sed 's/^20.\{18\}//g')
log "$RL"

# more --run-hook
POSTCERTTS=$(stat -c %y $CERTS/certificates/$DNSNAME.crt 2>&1)
[ $DBG ] && log "Post lego: $POSTCERTTS"
if [ "$PRECERTTS" != "$POSTCERTTS" ]; then
  [ $NOOPENSIPS ] && DEPLOYOPT=-o
  LEGO_CERT_DOMAIN=$DNSNAME \
  LEGO_CERT_KEY_PATH=$CERTS/certificates/$DNSNAME.key \
  LEGO_CERT_PATH=$CERTS/certificates/$DNSNAME.crt \
    /scripts/certdeploy.sh $DEPLOYOPT
fi

exit $RET
