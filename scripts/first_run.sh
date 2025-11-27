#!/bin/bash

SCR=$(basename $0)
log() {
  echo $(date "+%m-%d %H:%M:%S") [$$] $SCR - "$1"
  echo "$1" | logger -p local6.1 -t $SCR
}

# get global config
source /etc/opensips/globalcfg.sh
[ "$DEBUG" == "Y" ] && DBG=1

# db not yet exist?
if [ ! -e $DBPATH ]; then
  # create db
  log "Creating database $DBPATH"
  opensips-cli -o database_url=sqlite:// -o "database_modules=usrloc msilo presence tls_mgm" -o database_schema_path=/share/opensips -x database create $DBPATH
  chmod 770 $DBPATH
  chown opensips:mmsgateadm $DBPATH
else
  [ $DBG ] && log "Skipping database create, $DBPATH exists."
fi

# local CA not yet exist?
if [ ! -e $LOCCADIR/$LOCCACERT ]; then
  # create CA cert
  log "Making local root CA cert"
  CACFG=opensips-cli-caroot.cfg
  cat >$CACFG <<EOF
[default]
tls_ca_dir: $LOCCADIR
tls_ca_cert_file: $LOCCACERT
tls_ca_key_file: $LOCCAKEY
tls_ca_overwrite: yes
tls_ca_common_name: mmsGate CA
tls_ca_country: US
tls_ca_state: Florida
tls_ca_locality: Tally
tls_ca_organisation: https://github.com/RVgo4it
tls_ca_organisational_unit: https://github.com/RVgo4it/mmsgate
tls_ca_notafter: 315360000
tls_ca_key_size: 4096
tls_ca_md: SHA256
EOF

  opensips-cli --config $CACFG -x tls rootCA
  log "Checking local root CA cert"
  openssl x509 -noout -text -in $LOCCADIR/$LOCCACERT
  openssl rsa -check -noout -in $LOCCADIR/$LOCCAKEY
  openssl verify -CAfile $LOCCADIR/$LOCCACERT $LOCCADIR/$LOCCACERT
  rm $CACFG
else
  [ $DBG ] && log "Skipping local CA cert create, $LOCCADIR/$LOCCACERT exists."  
fi

# local user cert not yet exist?
if [ ! -e $LOCUSERDIR/$LOCUSERCERT ]; then

  # create local user cert
  USERCFG=opensips-cli-usercert.cfg
  log "Making local user cert"
  cat >$USERCFG <<EOF
[default]
tls_user_dir: $LOCUSERDIR
tls_user_cert_file: $LOCUSERCERT
tls_user_key_file: $LOCUSERKEY
tls_user_calist_file: $LOCUSERCALIST
tls_user_overwrite: yes
tls_user_cacert: $LOCCADIR/$LOCCACERT
tls_user_cakey: $LOCCADIR/$LOCCAKEY
tls_user_common_name: mmsGate
tls_user_country: US
tls_user_state: Florida
tls_user_locality: Tally
tls_user_organisation: https://github.com/RVgo4it
tls_user_organisational_unit: https://github.com/RVgo4it/mmsgate
tls_user_notafter: 315360000
tls_user_key_size: 4096
tls_user_md: SHA256
EOF

  opensips-cli -d --config $USERCFG -x tls userCERT
  log "Checking local user cert"
  openssl x509 -noout -text -in $LOCUSERDIR/$LOCUSERCERT
  openssl rsa -check -noout -in $LOCUSERDIR/$LOCUSERKEY
  openssl verify -CAfile $LOCCADIR/$LOCCACERT $LOCUSERDIR/$LOCUSERCERT
  rm $USERCFG

  # update the db with new local cert
  log "Updating tls_mgm table in db"
  sqlite3 $DBPATH "delete from tls_mgm where domain = 'local'"
  sqlite3 $DBPATH "insert into tls_mgm (verify_cert,domain,certificate,private_key,ca_list) values (0,'local',readfile('$LOCUSERDIR/$LOCUSERCERT'),readfile('$LOCUSERDIR/$LOCUSERKEY'),readfile('$LOCUSERDIR/$LOCUSERCALIST'));"

  # load the certs
  log "OpenSIPS reloading certs if already running"
  opensips-cli -x mi tls_reload

else
  [ $DBG ] && log "Skipping local user cert create, $LOCUSERDIR/$LOCUSERCERT exists."
fi
