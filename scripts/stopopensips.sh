#!/bin/bash
P=$(pgrep -x opensips)
if [ "$P" != "" ]; then
  /opt/venv/bin/opensips-cli -x mi kill
  sleep 5
  kill -KILL $P 2>/dev/null
fi
