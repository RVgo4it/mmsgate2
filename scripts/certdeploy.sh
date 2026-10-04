#!/bin/bash

# This script runs when lego creates or renews a certificate.  It is called by lego.

SCR=$(basename $0)
log() {
  echo $(date "+%m-%d %H:%M:%S") [$$] $SCR - "$1"
  echo "$1" | logger -p local6.1 -t $SCR
}

source /etc/opensips/globalcfg.sh
[ "$DEBUG" == "Y" ] && DBG=1

[ $DBG ] && log "Cert deploy started"
[ $DBG ] && log "Args: $@"
[ $DBG ] && log "DNSNAME: $DNSNAME"
[ $DBG ] && log "LEGO_HOOK_CERT_PATH: $LEGO_HOOK_CERT_PATH"
[ $DBG ] && log "LEGO_HOOK_CERT_KEY_PATH: $LEGO_HOOK_CERT_KEY_PATH"
[ $DBG ] && log "LEGO_HOOK_ISSUER_CERT_PATH: $LEGO_HOOK_ISSUER_CERT_PATH"

CERTS=/etc/opensips/tls

# refresh the sym link if needed
find $CERTS/certificates -type l -delete
[ ! -e $CERTS/certificates/$DNSNAME.crt ] && ln -fs $LEGO_HOOK_CERT_PATH $CERTS/certificates/$DNSNAME.crt
[ ! -e $CERTS/certificates/$DNSNAME.key ] && ln -fs $LEGO_HOOK_CERT_KEY_PATH $CERTS/certificates/$DNSNAME.key

[ $DBG ] && log "Sending signal to Nginx to reload network"
kill -HUP $(cat /run/nginx.pid)

[ $DBG ] && log "Importing certs into OpenSIPS databse for $DNSNAME"
sqlite3 $DBPATH "delete from tls_mgm where domain = '$DNSNAME'"
sqlite3 $DBPATH "insert into tls_mgm (type,verify_cert,require_cert,domain,certificate,private_key,ca_list) values (2,0,0,'$DNSNAME',readfile('$LEGO_HOOK_CERT_PATH'),readfile('$LEGO_HOOK_CERT_KEY_PATH'),readfile('$LEGO_HOOK_ISSUER_CERT_PATH'));"

log "ENABLEOPENSIPS=$ENABLEOPENSIPS"
[ "$ENABLEOPENSIPS" == "N" ] && exit 0

. /etc/profile
[ $DBG ] && log "OpenSIPS reloading certs"
log "$(opensips-cli -x mi tls_reload 2>&1)"

[ $DBG ] && log "OpenSIPS listing current certs"
[ $DBG ] && log "$(opensips-cli -x mi tls_list 2>&1)"

