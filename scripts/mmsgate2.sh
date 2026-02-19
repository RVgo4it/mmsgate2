#!/bin/bash
cd /scripts
for E in $(cat /etc/opensips/globalcfg.txt); do eval export $E; done
/scripts/mmsgate2

