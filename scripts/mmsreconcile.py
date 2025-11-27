#!/bin/python3

# MMSGate, a MMS gateway between Flexisip and VoIP.ms for use by Linphone clients.
# Copyright (C) 2024 by RVgo4it, https://github.com/RVgo4it
# Permission to use, copy, modify, and/or distribute this software for any purpose with or without
# fee is hereby granted.
# THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES WITH REGARD TO THIS
# SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE
# AUTHOR BE LIABLE FOR ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
# WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN ACTION OF CONTRACT,
# NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR PERFORMANCE
# OF THIS SOFTWARE.

import os
import requests
from datetime import timedelta, datetime, date

# command args for this script
args = [("--look-back",{"default":7,"type":int,"help":"Number of days to reconcile.  Default is 7."}),
  ("--did",{"default":None,"type":str,"help":"DID to limit search.  Default is None, or all DIDs."}),
  ("--debug",{"action":"store_true","help":"Display debug data to console."})]

# get all configs
from mmsgate import config_class
cfg = config_class(args)
_logger = cfg._logger

# days to look back
_logger.debug(str(("Look back days:",cfg.args.look_back)))
days_td = timedelta(days=cfg.args.look_back)

# need the API id/pw
apiid = cfg.get("api","apiid")
apipw = cfg.get("api","apipw")
did = cfg.args.did or ""

# need start date for REST query
startdate = (date.today() - days_td).isoformat()
_logger.info(str(("Reconcile since:",startdate)))

# URL for POST to resend messages if needed
webpath = cfg.get("web","protocol")+"://"+cfg.get("web","webdns")+":"+str(cfg.get("web","webport"))+cfg.get("web","pathpost")+"/"
_logger.debug(str(("Web Path:",webpath)))

# query old messages
url="https://voip.ms/api/v1/rest.php?api_username={}&api_password={}&method=getMMS&type=1&from={}&all_messages=1&did={}"
r = requests.get(url.format(apiid,apipw,startdate,did))
rslt = r.json()
if rslt["status"] != "success":
  _logger.error(str(("Error: GetMMS/SMS search failure:",r,r.text)))
  exit()

_logger.debug(str(("getMMS query result:",rslt)))

# conect to local DB for old messages
import sqlite3
try:
  dbfile = os.path.expanduser(cfg.get("mmsgate","dbfile"))
  conn = sqlite3.connect(dbfile)
except:
  _logger.error(str(("Opening DB file:",dbfile)))
  exit()

mcnt = 0
snt = 0
err = 0
# check each message
for msg in rslt["sms"]:
  mcnt += 1
  _logger.debug(str(("", msg["id"],msg["date"],msg["type"],msg["did"],msg["contact"],msg["message"])))
  _logger.debug(str(("sms element:",msg)))
  pmedia = []
  for media in msg["media"]:
    _logger.debug(str(("media:",media)))
    pmedia += [{"url":media}]
  _logger.debug(str(("POST media:",pmedia)))
  # we get this message before?  i.e. cnt > 0
  cnt, = conn.execute("SELECT COUNT(rowid) as msg_count FROM send_msgs WHERE msgid = ?;",(msg["id"],)).fetchone()
  _logger.debug(str(("SQLite3 DB query:",cnt)))
  # no, never got it before...
  if cnt == 0:
    snt += 1
    _logger.debug(str(("Reconsiling...",msg)))
    # data to send to MMSGATE
    json = {"data":{"payload":{"id":msg["id"],"from":{"phone_number":msg["contact"]},"to":[{"phone_number":msg["did"]}],"type":"MMS","text":msg["message"],"media":pmedia}}}
    _logger.debug(str(("JSON:",json)))
    # send it!
    r = requests.post(webpath, json=json)
    _logger.debug(str(("POST result:",r,r.text)))
    if r.status_code != 200: err += 1
  else:
    if cfg.args.debug: print("DEBUG:  Looks fine.")
_logger.info(str(("INFO: Messages:",str(mcnt),"Resent:",str(snt),"Errors:",str(err))))

