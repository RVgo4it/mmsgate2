#!/bin/bash
# This is the initial script called by dumb-init

SCR=$(basename $0)
log() {
  echo $(date "+%m-%d %H:%M:%S") [$$] $SCR - "$1"
  echo "$1" | logger -p local6.1 -t $SCR
}

source /etc/opensips/globalcfg.sh
[ "$DEBUG" == "Y" ] && DBG=1

echo
log "Init started!"

# some initial activity if first run
[ $DBG ] && log "Starting first_run.sh"
sudo -i /scripts/first_run.sh

# help pass the SIGTERM to child processes
signal_exit() {
  log "Received SIGTERM"
  kill -TERM $OPENSIPSPID
  kill -TERM $MMSGATEPID
}

trap signal_exit SIGTERM

# check for passed options
OPENSIPSOPTS=-F
MMSGATEOPTS=
while [ "$1" != "" ]; do 
  if [ "$1" == "-d" ] ; then DBG=1; fi
  if [ "$1" == "--opensipsdebug" ] ; then OPENSIPSOPTS="$OPENSIPSOPTS -D"; fi
  if [ "$1" == "--mmsgatedebug" ] ; then MMSGATEOPTS="$MMSGATEOPTS --mmsgate-logger=DEBUG"; fi
  shift
done

# check for arm32 - opensips bug w/ F_MALLOC
ARCH=$(uname -m)
if [[ "$ARCH" == armv7l* ]]; then
  OPENSIPSOPTS="$OPENSIPSOPTS -a Q_MALLOC"
fi

# start opensips
{ DONE=NO
  # catch signal sent from parent process
  sig_done() { DONE=YES; opensips-cli -x mi kill; }
  trap sig_done SIGTERM
  STARTEPOCH=0
  # loop until signaled
  while [ $DONE == NO ] ; do
    # prevent high cpu loop
    DURATION=$(($(date +%s) - STARTEPOCH))
    if [ $DURATION -lt 60 ]; then
      log "OpenSIPS loop sleeping 60"
      sleep 60
    fi
    source /etc/opensips/globalcfg.sh
    STARTEPOCH=$(date +%s)
    if [ $ENABLEOPENSIPS == Y ]; then
      log "Starting OpenSIPS"
      sudo -i -u opensips opensips $OPENSIPSOPTS -p /scripts/preproccfg.sh &
      PID=$!
      [ $DBG ] && log "Started OpenSIPS as $PID"
      wait $PID
      log "OpenSIPS ended..."
    else
      log "Skipping OpenSIPS"
    fi
  done
  log "OpenSIPS done!"
} &
OPENSIPSPID=$!
[ $DBG ] && log "OpenSIPS loop is PID $OPENSIPSPID"

# start mmsgate script
{ DONE=NO
  # catch signal sent from parent process
  sig_done() { DONE=YES; kill -TERM $PID; }
  trap sig_done SIGTERM
  STARTEPOCH=0
  # loop until signaled
  while [ $DONE == NO ] ; do
    # prevent high cpu loop
    DURATION=$(($(date +%s) - STARTEPOCH))
    if [ $DURATION -lt 60 ]; then
      log "MMSGate loop sleeping 60"
      sleep 60
    fi
    source /etc/opensips/globalcfg.sh
    STARTEPOCH=$(date +%s)
    if [ $ENABLEMMSGATE == Y ]; then
      log "Starting MMSGate"
      sudo -i -u mmsgate /scripts/mmsgate.py --default-values "apiid=$APIID;apipw=$APIPW;webdns=$DNSNAME;dbfile=$DBPATHM" $MMSGATEOPTS &
      PID=$!
      [ $DBG ] && log "Started MMSGate as $PID"
      wait $PID
      log "MMSGate ended..."
    else
      log "Skipping MMSGate"
    fi
  done
  log "MMSGate done!"
} &
MMSGATEPID=$!
[ $DBG ] && log "MMSGate loop is PID $MMSGATEPID"

# start rsyslog
[ $DBG ] && log "Starting rsyslog"
sudo rm /run/rsyslogd.pid
sudo -i rsyslogd
# start cron
[ $DBG ] && log "Starting cron"
sudo -i cron
# start nginx web service
[ $DBG ] && log "Config CPUs for Nginx"
sudo NGINX_ENTRYPOINT_WORKER_PROCESSES_AUTOTUNE=1 /scripts/30-tune-worker-processes.sh
[ $DBG ] && log "Starting Nginx"
sudo nginx -c /etc/opensips/nginx/nginx.conf

wait $OPENSIPSPID
wait $MMSGATEPID
sleep 3

