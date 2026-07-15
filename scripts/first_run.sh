#!/bin/bash

SCR=$(basename $0)
log() {
  echo $(date "+%m-%d %H:%M:%S") [$$] $SCR - "$1"
  echo "$1" | logger -p local6.1 -t $SCR
}

# get global config
source /etc/opensips/globalcfg.sh
[ "$DEBUG" == "Y" ] && DBG=1

# update?
if [ "$DBPATHU" == "" ]; then
  DBPATHU=/data/opensips/opensipsu.sqlite
  { sudo crontab -l; echo '50 2 * * 0 . /etc/opensips/globalcfg.sh; sqlite3 $DBPATHU "VACUUM;"'; } | sudo crontab -
  echo DBPATHU=/data/opensips/opensipsu.sqlite | sudo tee -a /etc/opensips/globalcfg.txt
fi

# random run for cert renew?
if crontab -l|grep /scripts/certs > /dev/null; then 
  [ $DBG ] && log "Certs renew is already scheduled..."
else 
  [ $DBG ] && log "Scheduling certs renew."
  M=$(perl -e 'print int(rand(60))')
  H1=$(perl -e 'print int(rand(12))')
  H2=$((H1+12))
  { crontab -l; echo "$M $H1,$H2 * * * /scripts/certs.sh renew"; } | crontab -
fi

# need update to opensips.cfg?
if ! diff /etc/opensips/opensips.cfg.md5sum /etc/opensips-bak/opensips.cfg.md5sum; then
  # check if opensips.cfg was modified
  md5sum /etc/opensips/opensips.cfg >/tmp/opensips.cfg.md5sum
  if diff /etc/opensips/opensips.cfg.md5sum /tmp/opensips.cfg.md5sum; then
    # copy over new/cfg from new image
    cp /etc/opensips-bak/opensips.cfg* /etc/opensips/
    [ $DBG ] && log "Installed newer files from /etc/opensips-bak"
  else
    log "NOTICE: Newer files in /etc/opensips-bak not installed.  Found opensips.cfg was modified."
  fi
fi

# db not yet exist?
if [ ! -e $DBPATH ]; then
  # create db
  log "Creating database $DBPATH"
  opensips-cli -o database_url=sqlite:// -o "database_modules=msilo presence tls_mgm" -o database_schema_path=/usr/share/opensips -x database create $DBPATH
  chmod 775 $DBPATH
  chown opensips:mmsgateadm $DBPATH
else
  [ $DBG ] && log "Skipping database create, $DBPATH exists."
fi

# db not yet exist?
if [ ! -e $DBPATHU ]; then
  # create db
  log "Creating database $DBPATHU"
  opensips-cli -o database_url=sqlite:// -o "database_modules=usrloc" -o database_schema_path=/usr/share/opensips -x database create $DBPATHU
  chmod 775 $DBPATHU
  chown opensips:mmsgateadm $DBPATHU
else
  [ $DBG ] && log "Skipping database create, $DBPATHU exists."
fi

# local CA not yet exist?
if [ ! -e $LOCCADIR/$LOCCACERT ]; then
  # create CA cert
  log "Making local root CA cert"

  mkdir -p $LOCCADIR/private
  openssl req \
    -new \
    -newkey rsa:4096 \
    -days 3650 \
    -nodes \
    -x509 \
    -subj '/CN=mmsGate CA/C=US/ST=Florida/L=Tally/O=Self' \
    -keyout $LOCCADIR/$LOCCAKEY \
    -out $LOCCADIR/$LOCCACERT

  log "Checking local root CA cert"
  openssl x509 -noout -text -in $LOCCADIR/$LOCCACERT
  openssl rsa -check -noout -in $LOCCADIR/$LOCCAKEY
  openssl verify -CAfile $LOCCADIR/$LOCCACERT $LOCCADIR/$LOCCACERT
else
  [ $DBG ] && log "Skipping local CA cert create, $LOCCADIR/$LOCCACERT exists."  
fi

# local user cert not yet exist?
if [ ! -e $LOCUSERDIR/$LOCUSERCERT ]; then

  log "Making local cert"
  mkdir -p $LOCUSERDIR/private
  openssl req \
    -new \
    -newkey rsa:4096 \
    -nodes \
    -subj '/CN=mmsGate/C=US/ST=Florida/L=Tally/O=Self' \
    -keyout $LOCUSERDIR/$LOCUSERKEY \
    -out $LOCUSERDIR/unsigned.csr

  openssl x509 \
    -req \
    -in $LOCUSERDIR/unsigned.csr \
    -CA $LOCCADIR/$LOCCACERT \
    -CAkey $LOCCADIR/$LOCCAKEY \
    -CAcreateserial \
    -days 3650 \
    -out $LOCUSERDIR/$LOCUSERCERT

  log "Checking local user cert"
  openssl x509 -noout -text -in $LOCUSERDIR/$LOCUSERCERT
  openssl rsa -check -noout -in $LOCUSERDIR/$LOCUSERKEY
  openssl verify -CAfile $LOCCADIR/$LOCCACERT $LOCUSERDIR/$LOCUSERCERT

  # update the db with new local cert
  log "Updating tls_mgm table in db"
  sqlite3 $DBPATH "delete from tls_mgm where domain = 'mmsGate'"
  sqlite3 $DBPATH "insert into tls_mgm (verify_cert,domain,certificate,private_key,ca_list) values (0,'mmsGate',readfile('$LOCUSERDIR/$LOCUSERCERT'),readfile('$LOCUSERDIR/$LOCUSERKEY'),readfile('$LOCUSERDIR/$LOCUSERCALIST'));"

  # load the certs
  log "OpenSIPS reloading certs if already running"
  opensips-cli -x mi tls_reload

else
  [ $DBG ] && log "Skipping local user cert create, $LOCUSERDIR/$LOCUSERCERT exists."
fi
