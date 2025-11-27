#!/bin/bash
# 
FILE=$1
LOGPATH=$2
MINUTES=$3
LINES=$4
{ tail -n $LINES -f $LOGPATH | sed -u -e 's/#012/\n/g' -e 's/#015//g' -e 's/#011//g' & }
PID=$!
sleep $MINUTES
kill $PID
sleep 10
rm $FILE
