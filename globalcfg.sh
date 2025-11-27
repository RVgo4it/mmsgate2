#!/bin/bash
gup() { GFILE=/etc/opensips/globalcfg.txt; TFILE=$GFILE.tmp; BFILE=$GFILE.bak; { while read LINE; do if [[ $LINE =~ ^$1= ]]; then echo $1="$2"; else echo $LINE; fi; done < $GFILE; } > $TFILE; mv $GFILE $BFILE; mv $TFILE $GFILE; };
get() { local NAME; local VAL; unset IFS; while read -r -d = NAME; read -r VAL; do eval $NAME="$VAL"; done </etc/opensips/globalcfg.txt; }
[ "$1" == "gup" ] && gup $2 $3
[ "$1" != "gup" ] && get

