#!/bin/bash

# This script runs when lego creates or renews a certificate.  It is called by lego.

[[ "$1" == "-o" ]] || OPENSIPS=1

SCR=$(basename $0)
log() {
  echo $(date "+%m-%d %H:%M:%S") [$$] $SCR - "$1"
  echo "$1" | logger -p local6.1 -t $SCR
}
log "$OPENSIPS"

source /etc/opensips/globalcfg.sh
[ "$DEBUG" == "Y" ] && DBG=1

[ $DBG ] && log "Cert deploy started"
[ $DBG ] && log "Args: $*"

CERTS=/etc/opensips/tls

[ $DBG ] && log "Sending signal to Nginx to reload network"
kill -HUP $(cat /run/nginx.pid)

[ $DBG ] && log "Importing certs into OpenSIPS databse for $LEGO_CERT_DOMAIN"
sqlite3 $DBPATH "delete from tls_mgm where domain = '$LEGO_CERT_DOMAIN'"
sqlite3 $DBPATH "insert into tls_mgm (type,verify_cert,require_cert,domain,certificate,private_key,ca_list) values (2,0,0,'$LEGO_CERT_DOMAIN',readfile('$LEGO_CERT_PATH'),readfile('$LEGO_CERT_KEY_PATH'),readfile('$CERTS/certificates/$LEGO_CERT_DOMAIN.issuer.crt'));"

. /etc/profile
[ $DBG ] && [ $OPENSIPS ] && log "OpenSIPS reloading certs"
[ $OPENSIPS ] && log "$(opensips-cli -x mi tls_reload 2>&1)"

[ $DBG ] && [ $OPENSIPS ] && log "OpenSIPS listing current certs"
[ $DBG ] && [ $OPENSIPS ] && log "$(opensips-cli -x mi tls_list 2>&1)"

