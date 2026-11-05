#!/bin/bash

# this will populate .htpasswdfile with file passwds from subacct table.  must run as root.

# paths to web password files
HTPW=/etc/opensips/nginx/.htpasswdfile
HTPWTMP=$HTPW.tmp
[ -e "$HTPWTMP" ] && rm "$HTPWTMP"
# path the acct list
ACCTSTMP=/tmp/accts.txt
# db query for accts and pw
. /etc/opensips/globalcfg.sh
sqlite3 $DBPATHM "SELECT account,uuid,filepasswd FROM subacct;" >$ACCTSTMP
# loop for each row of $ACCTSTMP
while IFS="|" read ACCT UUID FPW; do
  echo "$ACCT $FPW"
  # add pw to htpasswd file
  { echo -n "$ACCT:"; openssl passwd -apr1 "$FPW"; } | tee -a "$HTPWTMP" >/dev/null
done <$ACCTSTMP
# put the new htpasswd file in operation
mv "$HTPWTMP" "$HTPW"
echo done
# clean up
rm $ACCTSTMP


