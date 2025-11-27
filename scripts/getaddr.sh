#!/bin/bash -eE

# This script will get the IP address info; interface, MAC address, local address and public address

function __error_handing__(){
    local last_status_code=$1;
    local error_line_number=$2;
    echo 1>&2 "Error - exited with status $last_status_code at line $error_line_number";
    perl -slne 'if($.+5 >= $ln && $.-4 <= $ln){ $_="$. $_"; s/$ln/">" x length($ln)/eg; s/^\D+.*?$/\e[1;31m$&\e[0m/g;  print}' -- -ln=$error_line_number $0
}
trap  '__error_handing__ $? $LINENO' ERR

[[ $@ =~ "-d" ]] && DBG=1
[[ $@ =~ "-j" ]] && JSON=1

SCR=$(basename $0)
[[ $(type -t log) != function ]] && log() {
  [[ $JSON ]] || echo $(date "+%m-%d %H:%M:%S") [$$] $SCR - "$1"
  echo "$1" | logger -p local6.1 -t $SCR
}

source /etc/opensips/globalcfg.sh
[ "$DEBUG" == "Y" ] && DBG=1

PUBIPV4=0.0.0.0
PUBIPV6=0:0:0:0:0:0:0:0
PUBIPV6ALT=0:0:0:0:0:0:0:0

while read JIF; do
  # this loops for each network interface
  while read IFNAME && read ADDRESS && read OPERSTATE && read ADDR_INFO; do
    [ $DBG ] && log ">> $IFNAME $ADDRESS $OPERSTATE"
    [ $OPERSTATE != UP ] && continue
    # this loops for each address
    while read FAMILY && read LOCAL && read SCOPE; do
      [ $DBG ] && log "  >> $FAMILY $LOCAL $SCOPE"
      [ $SCOPE != global ] && continue
      # match suffix?
      if [ $FAMILY == inet6 ] && [ $PUBIPV6 == 0:0:0:0:0:0:0:0 ] && [[ "$LOCAL" =~ $IPV6SUFFIX$ ]]; then
        if PUBIPV6=$(curl -6 --interface $LOCAL -s https://icanhazip.com); then
          [ $DBG ] && log "      Got $PUBIPV6"
          declare IFIPV6=$IFNAME
          declare LOCIPV6=$LOCAL
          declare MACIPV6=$ADDRESS
        else
          declare PUBIPV6=0:0:0:0:0:0:0:0
        fi
      fi
      # get an IPv6 enen fs no suffix match
      if [ $FAMILY == inet6 ] && [ $PUBIPV6ALT == 0:0:0:0:0:0:0:0 ] && [ $PUBIPV6 == 0:0:0:0:0:0:0:0 ]; then
        if PUBIPV6ALT=$(curl -6 --interface $LOCAL -s https://icanhazip.com); then
          [ $DBG ] && log "      Got $PUBIPV6ALT"
          declare IFIPV6ALT=$IFNAME
          declare LOCIPV6ALT=$LOCAL
          declare MACIPV6ALT=$ADDRESS
        else
          declare PUBIPV6ALT=0:0:0:0:0:0:0:0
        fi
      fi
      # get IPv4 address too
      if [ $FAMILY == inet ] && [ $PUBIPV4 == 0.0.0.0 ]; then 
        if PUBIPV4=$(curl -4 --interface $LOCAL -s https://icanhazip.com); then
          [ $DBG ] && log  "      Got $PUBIPV4"
          IFIPV4=$IFNAME
          LOCIPV4=$LOCAL
          MACIPV4=$ADDRESS
        else
          PUBIPV4=0.0.0.0
        fi
      fi
    done < <(echo $ADDR_INFO | jq -r ".[] | (.family, .local, .scope)")
  done < <( echo $JIF| jq -cr ".ifname, .address, .operstate, .addr_info" )
done < <( ip -j addr show | jq -c ".[]" )

if [ $PUBIPV6 == 0:0:0:0:0:0:0:0 ] && [ $PUBIPV6ALT != 0:0:0:0:0:0:0:0 ]; then
  [ $DBG ] && log "Alternate IPv6 used, no suffix match for $IPV6SUFFIX"
  PUBIPV6=$PUBIPV6ALT
  IFIPV6=$IFIPV6ALT
  LOCIPV6=$LOCIPV6ALT
  MACIPV6=$MACIPV6ALT
fi

# -j is for JSON format output
[[ $JSON ]] && echo '{"ipv4":{"if":"'$IFIPV4'","mac":"'$MACIPV4'","local":"'$LOCIPV4'","public":"'$PUBIPV4'"},"ipv6":{"if":"'$IFIPV6'","mac":"'$MACIPV6'","local":"'$LOCIPV6'","public":"'$PUBIPV6'"}}'

[ $DBG ] && log "IPV4 $IFIPV4 $MACIPV4 $LOCIPV4 $PUBIPV4"
[ $DBG ] && log "IPV6 $IFIPV6 $MACIPV6 $LOCIPV6 $PUBIPV6"
if [ "$0" == "$BASH_SOURCE" ]; then
  exit 0
else
  return 0
fi

