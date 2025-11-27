#!/bin/bash

# this script checks the current dns names and compares to public IP addresses.  if wrong, updates dns.  runs as root.

[ "$1" == "-d" ] && DBG=1

# get global config
source /etc/opensips/globalcfg.sh
[ "$DEBUG" == "Y" ] && DBG=1

SCR=$(basename $0)
log() {
  echo $(date "+%m-%d %H:%M:%S") [$$] $SCR - "$1"
  echo "$1" | logger -p local6.1 -t $SCR
}

[ "$DNSNAME" == "" ] && { log "Missing global value for DNSNAME"; exit 1; }
[ "$DNSTOKEN" == "" ] && { log "missing global value for DNSTOKEN"; exit 1; }

# get public IP addresses
source /scripts/getaddr.sh $1
[ $DBG ] && log "Checking domain $DNSNAME"

DOMAIN_IPV4=$(dig +noall $DNSNAME A +short) || { log "Dig error for $DNSNAME A"; exit 1; }
DOMAIN_IPV6=$(dig +noall $DNSNAME AAAA +short) || { log "Dig error for $DNSNAME AAAA"; exit 1; }
[ $DBG ] && log "Domain IPv4 $DOMAIN_IPV4"
[ $DBG ] && log "Domain IPv6 $DOMAIN_IPV6"

dns_update() {
  IP4U=$1 IP6U=$2
  [ $DBG ] && log "Called dns_update with $IP4U and $IP6U"
  [ $DBG ] && log "API call /dns/getroot/$DNSNAME"
  RET=$(curl -s -X GET "https://api.dynu.com/v2/dns/getroot/$DNSNAME" -H "accept: application/json" -H "API-Key: $DNSTOKEN") || { log "curl API failed"; exit 1; }
  [ $DBG ] && log "Returned: $RET"
  [ "$(echo $RET| jq '.statusCode')" != "200" ] && { log "curl API failed" ;exit 1; }
  DOMID=$(echo $RET| jq ".id")
  [ $DBG ] && log "Domain ID is $DOMID"
  [ $DBG ] && log "API call /dns/$DOMID"
  RET=$(curl -s -X GET "https://api.dynu.com/v2/dns/$DOMID" -H "accept: application/json" -H "API-Key: $DNSTOKEN") || { log "curl API failed" ;exit 1; }
  [ $DBG ] && log "Returned: $RET"
  [ "$(echo $RET| jq '.statusCode')" != "200" ] && { log "curl API failed" ;exit 1; }
  while read DOM; read GRP; read IP4A; read IP6A; read TTL; read IP4; read IP6; read IP4W; read IP6W; do break; done < <( echo $RET|jq ".name, .group, .ipv4Address, .ipv6Address, .ttl, .ipv4, .ipv6, .ipv4WildcardAlias, .ipv6WildcardAlias"; )
  [ $DBG ] && log "Got DOM=$DOM IP4A=$IP4A IP6A=$IP6A TTL=$TTL IP4=$IP4 IP6=$IP6 IP4W=$IP4 IP6W=$IP6"
  [ "$DOM" != "\"$DNSNAME\"" ] && { log "curl return check failed"; exit 1; }
  POST='{
    "name": '$DOM',
    "group": '$GRP',
    "ipv4Address": '$IP4U',
    "ipv6Address": '$IP6U',
    "ttl": '$TTL',
    "ipv4": '$IP4',
    "ipv6": '$IP6',
    "ipv4WildcardAlias": '$IP4W',
    "ipv6WildcardAlias": '$IP6W',
    "allowZoneTransfer": false,
    "dnssec": false
    }'
  [ $DBG ] && log "API POST call /dns/$DOMID with data: $POST"
  RET=$(curl -s -X POST "https://api.dynu.com/v2/dns/$DOMID" -H "accept: application/json" -H "API-Key: $DNSTOKEN" -H "Content-Type: application/json" -d "$POST") || { log "curl API failed" ;exit 1; }
  [ $DBG ] && log "Returned: $RET"
  [ "$(echo $RET| jq '.statusCode')" != "200" ] && { log "curl API failed" ;exit 1; }
  exit 0
}

case $IPADDRSUP in
  BOTH)
    if [ "$DOMAIN_IPV4" != "$PUBIPV4" ] || [ "$DOMAIN_IPV6" != "$PUBIPV6" ]; then
      log "IP address disparity, public vs domain.  Public IPv4 $PUBIPV4 and IPv6 $PUBIPV6 vs domain IPv4 $DOMAIN_IPV4 and IPv6 $DOMAIN_IPV6"
      if dns_update \"$PUBIPV4\" \"$PUBIPV6\"; then
        [ $DBG ] && log "Request successful!"
      else
        log "Failed to update domain addresses"
        exit 1
      fi
    fi
    ;;
  IPV4)
    if [ "$DOMAIN_IPV4" != "$PUBIPV4" ] || [ "$DOMAIN_IPV6" != "" ]; then
      log "IP address disparity, public vs domain.  Public IPv4 $PUBIPV4 vs domain IPv4 $DOMAIN_IPV4"
      if dns_update \"$PUBIPV4\" null; then
        [ $DBG ] && log "Request successful!"
      else
        log "Failed to update domain addresses"
        exit 1
      fi
    fi
    ;;
  IPV6)
    if [ "$DOMAIN_IPV4" != "" ] || [ "$DOMAIN_IPV6" != "$PUBIPV6" ]; then
      log "IP address disparity, public vs domain.  Public IPv4 $PUBIPV4 and IPv6 $PUBIPV6 vs domain IPv4 $DOMAIN_IPV4 and IPv6 $DOMAIN_IPV6"
      if dns_update null \"$PUBIPV6\"; then
        [ $DBG ] && log "Request successful!"
      else
        log "Failed to update domain addresses"
        exit 1
      fi
    fi
    ;;
esac

exit 0

