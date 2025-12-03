#!/bin/bash

# This script is called by OpenSIPS when started.  It processes the config file via stdin/stdout.  It runs as user opensips.

SCR=$(basename $0)
log() {
#  echo $(date "+%m-%d %H:%M:%S") [$$] $SCR - $1
  echo "$1" | logger -p local6.1 -t $SCR
}

# get global config
source /etc/opensips/globalcfg.sh
[ "$DEBUG" == "Y" ] && DBG=1

# get public IP addresses
source /scripts/getaddr.sh

unset SEDOPTS
SEDOPTS="$SEDOPTS s=__DNSNAME__=$DNSNAME=;"
SEDOPTS="$SEDOPTS s=__DBPATH__=$DBPATH=;"
SEDOPTS="$SEDOPTS s=__DBPATHM__=$DBPATHM=;"
SEDOPTS="$SEDOPTS s=__APIID__=$APIID=;"
# password may contain an = so escape it
SEDOPTS="$SEDOPTS s=__APIPW__=${APIPW//=/\\=}=;"

# check the IPv6 supported and replace as needed
if [ $IPADDRSUP == BOTH ]; then
    # errors getting pub ips?
    if [ "$PUBIPV6" != "0:0:0:0:0:0:0:0" ]; then
        [ $DBG ] && log "Processing IPv6 $PUBIPV6:5061"
        SEDOPTS="$SEDOPTS s/#socket=tls:\\[0:0:0:0:0:0:0:0]:5061/socket=tls:[$PUBIPV6]:5061/;"
        SEDOPTS="$SEDOPTS s/dns_try_ipv6=no/dns_try_ipv6=yes/;"
    else
        log "Error getting public IPV6"
    fi
fi

# check module path - default is 32 bit /usr/lib/opensips/modules
if [ -e /usr/lib64/opensips/modules ]; then
    SEDOPTS="$SEDOPTS s=/usr/lib/opensips/modules=/usr/lib64/opensips/modules=;"
fi

# IPv4 always
if [ "$PUBIPV4" != "0.0.0.0" ]; then
    # no NAT router?
    if [ "$PUBIPV4" == "$LOCIPV4" ]; then
#        SEDOPTS="$SEDOPTS s/socket=tls:0.0.0.0:5061/socket=tls:$LOCIPV4:5061/;"
#        [ $DBG ] && log "Processing IPv4 $LOCIPV4:5061"
    else
        SEDOPTS="$SEDOPTS s/socket=tls:0.0.0.0:5061/socket=tls:0.0.0.0:5061\ as\ $PUBIPV4:5061/;"
        [ $DBG ] && log "Processing IPv4 0.0.0.0:5061 as $PUBIPV4:5061"
    fi
# errors getting pub ips?
else
    log "Error getting public IPV4s"
fi

SEDOPTS="{ $SEDOPTS }"
[ $DBG ] && log "SEDOPTS = $SEDOPTS"

# do the replacements
if sed "$SEDOPTS"; then
    [ $DBG ] && log "sed replaced..."
else
    log "sed error..."
fi

# remember the configured ip addresses so as to see if they changed later
if printf "PRECFGPUBIPV4=$PUBIPV4\nPRECFGPUBIPV6=$PUBIPV6\n" > /home/opensips/precfg.sh; then
  [ $DBG ] && log "Placed $PUBIPV4 and $PUBIPV6 in /home/opensips/precfg.sh"
else
  log "Error placing $PUBIPV4 and $PUBIPV6 in /home/opensips/precfg.sh"
fi

