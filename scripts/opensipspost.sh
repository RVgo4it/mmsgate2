#!/bin/bash

# this script is to be called from opensips and performs a rest api call to linphone.org.
# it is used for push notification.  it is fired and forgetten.  no return data.
# data is passed via OSIPS_EXEC_n env vars.  0 is the apikey for auth to linphone.org.
# 1 is the JSON data for the push notification.  2 is the linphone acct name.  3 is
# the current xlog level.

# L_ALERT - log level -3
# L_CRIT - log level -2
# L_ERR - log level -1
# L_WARN - log level 1
# L_NOTICE - log level 2
# L_INFO - log level 3
# L_DBG - log level 4
XLOGLVL=$OSIPS_EXEC_3

[[ $XLOGLVL -ge 4 ]] && logger -p local7.debug --id=$$ -t opensipspost.sh "Starting..."
[[ $XLOGLVL -ge 4 ]] && logger -p local7.debug --id=$$ -t opensipspost.sh "OSIPS_EXEC_0 = $OSIPS_EXEC_0"
[[ $XLOGLVL -ge 4 ]] && logger -p local7.debug --id=$$ -t opensipspost.sh "OSIPS_EXEC_1 = $OSIPS_EXEC_1"
[[ $XLOGLVL -ge 4 ]] && logger -p local7.debug --id=$$ -t opensipspost.sh "OSIPS_EXEC_2 = $OSIPS_EXEC_2"
[[ $XLOGLVL -ge 4 ]] && logger -p local7.debug --id=$$ -t opensipspost.sh "OSIPS_EXEC_3 = $OSIPS_EXEC_3"

# Linphone acct
LINPHONE=$OSIPS_EXEC_2

# JSON request
JSONREQ="$OSIPS_EXEC_1"

# APIKEY
APIKEY=$OSIPS_EXEC_0

# use a lock file to prevent simultaneous parallel push notifications for same client
LOCFILE=/tmp/$LINPHONE.lock
{
  # wait up to 10 seconds.  curl normally only takes a few seconds.
  flock -w 10 9 && {

    [[ $XLOGLVL -ge 4 ]] && logger -p local7.debug --id=$$ -t opensipspost.sh "Posting data..."

    RET=$( curl -v --fail-with-body --digest -X POST -H "x-api-key: $APIKEY" -H "content-type: application/json" -H "accept: application/json" \
      -d "$JSONREQ" \
      'https://subscribe.linphone.org/api/push_notification' )
    RC=$?

    if [[ $RC -eq 22 ]] ; then
      [[ $XLOGLVL -ge -1 ]] && logger -p local7.err --id=$$ -t opensipspost.sh "Curl returned an error. Result = $RET"
    fi

  # wait timed out...
  } || {
    [[ $XLOGLVL -ge -1 ]] && logger -p local7.err --id=$$ -t opensipspost.sh "File lock not released within 10 seconds"
  }

} 9>$LOCFILE

[[ $XLOGLVL -ge 4 ]] && logger -p local7.debug --id=$$ -t opensipspost.sh "RC = $RC Result = $RET"
[[ $XLOGLVL -ge 4 ]] && logger -p local7.debug --id=$$ -t opensipspost.sh "Done!"
