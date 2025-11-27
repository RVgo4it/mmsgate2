#!/bin/bash

# This script checks for IP addresses changes from when OpenSIPS was started.  If changed, OpenSIPS must be restarted.  Must ran as root.

[ "$1" == "-d" ] && DBG=1

SCR=$(basename $0)
log() {
  echo $(date "+%m-%d %H:%M:%S") [$$] $SCR - $1
  echo "$1" | logger -p local6.1 -t $SCR
}

# get global config
source /etc/opensips/globalcfg.sh
[ "$DEBUG" == "Y" ] && DBG=1

if [ $ENABLEOPENSIPS == N ]; then
  [ $DBG ] && log "OpenSIPS not enabled.  Skipping."
  exit 0
fi

[ -e /home/opensips/precfg.sh ] || { log "OpenSIPS used IP addresses not found.  Skipping."; exit 1; }

# get public IP addresses
source /scripts/getaddr.sh $1
[ $DBG ] && log "Current Public IPv4: $PUBIPV4"
[ $DBG ] && log "Current Public IPv6: $PUBIPV6"

# get the addresses used at opensips start
source /home/opensips/precfg.sh
[ $DBG ] && log "OpenSIPS Public IPv4: $PRECFGPUBIPV4"
[ $DBG ] && log "OpenSIPS Public IPv6: $PRECFGPUBIPV6"

KILL=NO

# check the IP versions supported and compare current and when started.
case $IPADDRSUP in
  BOTH)
    # errors getting pub ips?
    if [ "$PUBIPV4" != "0.0.0.0" ] && [ "$PUBIPV6!" != "0:0:0:0:0:0:0:0" ]; then
      if [ "$PRECFGPUBIPV4" != "$PUBIPV4" ] || [ "$PRECFGPUBIPV6" != "$PUBIPV6" ]; then
        KILL=YES
      fi
    else
      log "Error getting public IPs"
    fi
    ;;
  IPV4)
    # errors getting pub ips?
    if [ "$PUBIPV4" != "0.0.0.0" ]; then
      if [ "$PRECFGPUBIPV4" != "$PUBIPV4" ]; then 
        KILL=YES
      fi
    else
      log "Error getting public IPs"
    fi
    ;;
  IPV6)
    # errors getting pub ips?
    if [ "$PUBIPV6!" != "0:0:0:0:0:0:0:0" ]; then
      if [ "$PRECFGPUBIPV6" != "$PUBIPV6" ]; then 
        KILL=YES
      fi
    else
      log "Error getting public IPs"
    fi
    ;;
esac

# if addresses changed, stop opensips (it will auto restart)
if [ "$KILL" == "YES" ]; then
  log "IP addresses changed!  Stopping OpenSIPS..."
#  log "$(opensips-cli -x mi kill 2>&1)"
  log "$( { P=$(pgrep opensips); opensips-cli -x mi kill; sleep 5; sudo kill -KILL $P ; } 2>&1 )"
else
  [ $DBG ] && log "Addresses look good..."
fi
