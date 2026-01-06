#!/bin/python3

# MMSGate, a MMS gateway between OpenSIPS and VoIP.ms for use by Linphone clients.
# Copyright (C) 2024 by RVgo4it, https://github.com/RVgo4it
# Permission to use, copy, modify, and/or distribute this software for any purpose with or without
# fee is hereby granted.
# THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES WITH REGARD TO THIS
# SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE
# AUTHOR BE LIABLE FOR ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
# WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN ACTION OF CONTRACT,
# NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR PERFORMANCE
# OF THIS SOFTWARE.

# v1.0.13 1/5/2026 Fixes to client config section
# v1.0.12 1/4/2026 Applied ruff fixes
# v1.0.11 1/3/2026 New python3-multipart required rewrite upload service plus crontab enhancements
# v1.0.10 1/2/2026 Minor bug fixes and Wizard enhancements
# v1.0.9 12/26/2025 Improved firewall testing in wazard
# v1.0.8 12/24/2025 Minor bugs and wizard fixes.
# v1.0.7 12/5/2025 Added password change for /admin and minor bugs
# v1.0.6 12/3/2025 Added fix for /admin password and 32 vs 64 /usr
# v1.0.5 12/2/2025 Added more Windows support
# v1.0.4 11/28/2025 Bug fix in wizard
# v1.0.3 11/27/2025 Extra security for admin page
# v1.0.2 11/27/2025 Minor fixes in wizard and Voip.ms sub acct admin
# v1.0.1 11/26/2025 Switched to OpenSIPS v3.6, added check in FW test and added OpenSIPS auto_scaling_profile
# v1.0.0 11/19/2025 Major rewrite for OpenSIPS and Push Notification via linphone.org

# some of the usual imports
import os
import sys
import time

# this class runs the httpd/wsgi processes for receiving http(s) requests
class web_class():
    import gunicorn.app.base
    fifodir = "/tmp/fifolog"

    # this is the standard class for starting gunicorn
    class StandaloneApplication(gunicorn.app.base.BaseApplication):
        def __init__(self, app, options=None):
            self.options = options or {}
            self.application = app
            super().__init__()
        def load_config(self):
            config = {key: value for key, value in self.options.items()
                      if key in self.cfg.settings and value is not None}
            for key, value in config.items():
                self.cfg.set(key.lower(), value)
        def load(self):
            return self.application

    # init for web class
    def __init__(self,ask_q,resp_q,loglvl_q):
        import multiprocessing
        import subprocess
        # return calc for number of processes for gunicorn
        def number_of_workers():
            try:
                # docker run --cpus is available via nproc cmd
                result = subprocess.run(['nproc'], capture_output=True, text=True)
                cores = int(result.stdout.strip())
            except ValueError:
                # fail over to cpu_count()
                cores = multiprocessing.cpu_count()
            return (cores * 2) + 1
        # prevents error message when worker ends
        def post_worker_init(worker):
            import atexit
            from multiprocessing.util import _exit_function
            atexit.unregister(_exit_function)
            _logger.debug("worker post_worker_init done, (pid: {})".format(worker.pid))
        # gunicorn options
        options = {
            'timeout': 0,
            'workers': number_of_workers(),
            'post_worker_init':post_worker_init,
            'worker_tmp_dir': '/dev/shm',
            'syslog': True,
            'syslog_addr': "unix:///dev/log#dgram",
            'syslog_prefix': "mmsgate",
            'syslog_facility': "local5",
            'loglevel': cfg.loglvltext
        }
        # bind override to allow proxy like nginx?
        if cfg.exists("web","bindoverride"):
            options["bind"] = cfg.get("web","bindoverride")
        else:
            # ok, bind per config and maybe tls
            options["bind"] = '%s:%s' % (cfg.get("web","webbind"), str(cfg.get("web","webport")))
            # tls?
            if cfg.get("web","protocol") == "https":
                if cfg.exists("web","cert") and cfg.exists("web","cert"):
                    cert = cfg.get("web","cert")
                    key = cfg.get("web","key")
                    options["certfile"] = cert
                    options["keyfile"] = key
                else:
                    raise ValueError("For https protocol, section/option of web/cert and web/key are required in config file.  Please correct.")
        # init for web request/answer queues
        self.loglvl_q = loglvl_q
        self.resp_q = resp_q
        self.ask_q = ask_q
        # prep the gunicorn app and web thread
        self.app = web_class.StandaloneApplication(self.webhook_app, options)

    # start the http server thread and gunicorn processes
    def start(self):
        self.app.run()

    # process the webhook POST or the MMS media GET or file upload or admin interface...
    def webhook_app(self, environ, start_response):
        import json
        import mimetypes
        import os
        import uuid
        from datetime import datetime, timedelta, timezone
        from urllib.parse import parse_qs, urlparse
        from urllib.request import quote

        import requests
        # template for sending MMS to linphone clients
        mms_template = '''<?xml version="1.0" encoding="UTF-8"?>
<file xmlns="urn:gsma:params:xml:ns:rcs:rcs:fthttp" xmlns:am="urn:gsma:params:xml:ns:rcs:rcs:rram">
<file-info type="file">
<file-size>{}</file-size>
<file-name>{}</file-name>
<content-type>{}</content-type>
<data url="{}" until="{}"/>
</file-info>
</file>'''
        # get current log level and set it
        self.ask_q.put(("GetLogLevel",))
        curlvl = self.loglvl_q.get()
        _logger.setLevel(curlvl)
        # get the httpd request params
        path    = environ["PATH_INFO"]
        method  = environ["REQUEST_METHOD"]
        # maybe a POST for file server? (extra security check that it's a post from registered linphone client or local network)
        if method == "POST" and path.startswith(cfg.get("web","pathfile")) and ("voip.ms" in environ.get("HTTP_FROM","") or ":38000" in environ.get("HTTP_HOST","")):
            _logger.debug("WEB App: "+method+" "+path)
            _logger.debug("POST to server:"+str(environ))
            try:
                # content length will tell us if initial conect or file upload
                if "CONTENT_LENGTH" in environ and environ["CONTENT_LENGTH"] != "":
                    request_body_size = int(environ["CONTENT_LENGTH"])
                else:
                    request_body_size = 0
                # just init contact
                if request_body_size == 0:
                    # return 204
                    status = "204 No Content"
                    response_body = b""
                # return 200 if got file POST
                else:
                    from multipart import is_form_request, parse_form_data
                    resp = ""
                    # it is a multipart form
                    if is_form_request(environ):
                        _logger.debug(str(("is_form_request","true")))
                        forms, files = parse_form_data(environ)
                        # get place to put file(s)
                        path = os.path.expanduser(cfg.get("web","localmedia"))
                        udir = str(uuid.uuid4())
                        dpath = os.path.join(path,udir)
                        os.makedirs(dpath,exist_ok=True)
                        until = (datetime.now(tz=timezone.utc)+timedelta(days=365)).isoformat()[:19]+"Z"
                        # check each file, should only be 1?
                        for fstr in files:
                            f = files[fstr]
                            # save it to disk
                            fpath = os.path.join(dpath,f.filename)
                            _logger.debug(str(("Local file path:",fstr,fpath)))
                            f.save_as(fpath)
                            # get info for XML file
                            filesize = os.path.getsize(fpath)
                            filetype = mimetypes.guess_type(fpath)[0]
                            fname = os.path.join(udir,f.filename)
                            furl = cfg.get("web","protocol") + "://" + cfg.get("web","webdns") + ":" + str(cfg.get("web","webport")) + cfg.get("web","pathget") + "/" + quote(fname)
                            _logger.debug(str(("URL file path:",furl)))
                            # got XML file to return
                            resp += mms_template.format(filesize,f.filename,filetype,furl,until)
                    # no file POST found?
                    if resp == "":
                        raise ValueError("No POST-ed file found.")
                    else:
                        status = "200 OK"
                        response_body = str.encode(resp)
                    # check for "to" url param
                    if environ['QUERY_STRING'] != "":
                        import urllib
                        # a "to" param was passed in upload url.  send to it.
                        qs = urllib.parse.parse_qs(environ['QUERY_STRING'])
                        if "to" in qs:
                            _logger.debug("POST to fileserver: sending: "+str(qs))
                            # send image/video uploaded to the client soecified
                            self.ask_q.put_nowait(("MsgNew","1099",None,qs["to"][0],None,resp,"IN",None,"MMS",0))
            except Exception as e:
                PrintException(e)
                status = "500 Error"
                response_body = b"Internal error"
            else:
                pass
            finally:
                _logger.debug("Returning: "+status)
                headers = [("Content-type", "text/plain"),
                    ("Content-Length", str(len(response_body)))]
                start_response(status, headers)
                return [response_body]
        # maybe web hook POST from voip.ms
        if method == "POST" and path.startswith(cfg.get("web","pathpost")):
            _logger.debug("WEB App: "+method+" "+path)
            _logger.debug("POST to server:"+str(environ))
            try:
                # the web hook data is in JSON format
                request_body_size = int(environ["CONTENT_LENGTH"])
                request_body = environ["wsgi.input"].read(request_body_size)
                str_body = request_body.decode("utf-8")
                _logger.debug("Web hook Body: "+str_body)
                j = json.loads(str_body)
                payload = j["data"]["payload"]
                _logger.debug("Object payload: "+str(payload))
                # array of MMS messages
                mms_msaages = []
                # download each media to make available as GET later
                for media in payload["media"]:
                    _logger.debug("URL: "+media["url"])
                    r = requests.get(media["url"], stream=True)
                    if r.ok:
                        # the media file will be same name in a UUID dir
                        fname = os.path.split(urlparse(media["url"]).path)[1].lower()
                        udir = str(uuid.uuid4())
                        dpath = os.path.join(cfg.get("web","localmedia"),udir)
                        os.makedirs(dpath,exist_ok=True)
                        fpath = os.path.join(dpath,fname)
                        _logger.debug("Local path: "+fpath)
                        with open(fpath, "wb") as f:
                            for chunk in r.iter_content(chunk_size=1024 * 8):
                                if chunk:
                                    f.write(chunk)
                                    f.flush()
                                    os.fsync(f.fileno())
                        # fill in the XML template for the MMS message
                        filesize = os.path.getsize(fpath)
                        filetype = r.headers["Content-Type"]
                        furl = cfg.get("web","protocol") + "://" + cfg.get("web","webdns") + ":" + str(cfg.get("web","webport")) + cfg.get("web","pathget") + "/" + udir + "/" + fname
                        _logger.debug("New URL: "+furl)
                        # assume one year
                        until = (datetime.now(tz=timezone.utc)+timedelta(days=365)).isoformat()[:19]+"Z"
                        mms_msaages.append(mms_template.format(filesize,fname,filetype,furl,until))
                        _logger.debug("MMS Message: "+mms_msaages[-1])
                    else:
                        _logger.error("URL download failed: "+media["url"])
                # need the did_accts to find who gets a copy of the message
                _logger.debug("Requesting did_accts from API process-thread: "+str(self.ask_q))
                self.ask_q.put(("GetAccts",))
                _logger.debug("Getting did_accts from API process-thread: "+str(self.resp_q))
                did_accts = self.resp_q.get()
                _logger.debug("Got did_accts from API process-thread"+str(did_accts))
                # the to (destination) is a DID. we'll use the CID setting from voip.ms for the sub account to receive.
                for todid in payload["to"]:
                    if did_accts and todid["phone_number"] in did_accts.keys():
                        # send it (SMS or MMS) to every sub account using the DID as CID.
                        for toid in did_accts[todid["phone_number"]]:
                            # SMS message?
                            if payload["type"] == "SMS":
                                self.ask_q.put_nowait(("MsgNew",payload["from"]["phone_number"],None,toid,None,payload["text"],"IN",todid["phone_number"],"SMS",payload["id"]))
                            # must be MMS
                            else:
                                if payload["text"] != "":
                                    self.ask_q.put_nowait(("MsgNew",payload["from"]["phone_number"],None,toid,None,payload["text"],"IN",todid["phone_number"],"SMS",payload["id"]))
                                for mmsmsg in mms_msaages:
                                    self.ask_q.put_nowait(("MsgNew",payload["from"]["phone_number"],None,toid,None,mmsmsg,"IN",todid["phone_number"],"MMS",payload["id"]))
                    else:
                        _logger.warning("The DID "+todid["phone_number"]+" not found in API's did_accts.keys(): "+str(did_accts.keys()))
            # something very wrong
            except Exception as e:
                PrintException(e)
                # return 500 Error
                status = "500 Error"
                response_body = b"Internal error"
            else:
                # return 200 OK
                status = "200 OK"
                response_body = b"ok"
            finally:
                headers = [("Content-type", "text/plain"),
                    ("Content-Length", str(len(response_body)))]
                start_response(status, headers)
                return [response_body]

        # admin - reject if it was not from ports 38000 (via 'admin' site of Nginx) or port 38080 (via direct local)
        if path.startswith(cfg.get("web","pathadmin")) and (environ.get("HTTP_HOST","").endswith(":38000") or environ.get("HTTP_HOST","").endswith("127.0.0.1:38080")):
            try:
                import os
                # get params in url, if any
                d = parse_qs(environ['QUERY_STRING'])
                # must be just an admin page request via POST or GET and no url param 'log'
                if "log" not in d:
                    _logger.debug("WEB App: "+method+" "+path)
                    _logger.debug("POST/GET to server:"+str(environ))
                    response_body = self.admin(environ,self.ask_q)
                    status = "200 OK"
                    headers = [("Content-type", "text/html"),
                            ("Content-Length", str(len(response_body)))]
                # must be a log file request
                else:
                    # open the fifo from other process running tail
                    try:
                        f = os.open(self.fifodir + '/' + d['log'][0], os.O_RDONLY | os.O_NONBLOCK)
                    except FileNotFoundError:
                        # if not found, must be done
                        response_body = b'--DONE--'
                    else:
                        # read a chunk of the log from fifo
                        try:
                            response_body = os.read(f,1024*1024)
                        except BlockingIOError:
                            # must have been none
                            response_body = b''

                    # return the progress from tail
                    status = "200 OK"
                    headers = [("Content-type", "text/plain")]

                start_response(status, headers)
                return [response_body]

            except Exception as e:
                PrintException(e)
                status = "500 Error"
                response_body = b"Internal error"
            else:
                pass

        # not the right paths for GET/POST
        response_body = b"Oops... missing something!"
        status = "404 Not found"
        headers = [("Content-type", "text/plain"),
              ("Content-Length", str(len(response_body)))]
        start_response(status, headers)
        return [response_body]

    # return a dict of values from POST data
    def get_post_data(self,environ):
        import urllib
        qs={}
        if environ["REQUEST_METHOD"] == "POST":
            if "CONTENT_LENGTH" in environ and environ["CONTENT_LENGTH"] != "":
                request_body_size = int(environ["CONTENT_LENGTH"])
            else:
                request_body_size = 0
            if request_body_size != 0:
                request_body = environ["wsgi.input"].read(request_body_size)
                qs = urllib.parse.parse_qs(request_body.decode("utf_8"))
                for k,v in qs.items():
                    if type(v) in (tuple, list):
                        qs[k] = v[0]
        return qs

    # process the admin forms
    def admin(self,environ,ask_q):
        import html
        import json
        import secrets
        import string
        import subprocess
        import urllib

        import requests
        from prettytable import PrettyTable
        from wtforms import (
            BooleanField,
            FieldList,
            Form,
            FormField,
            HiddenField,
            PasswordField,
            SelectField,
            StringField,
            SubmitField,
        )
        _logger.debug(str(("Admin method environ:",environ)))

        # some things do not work w/ Lynx browser
        lynx = 'Lynx' in environ.get('HTTP_USER_AGENT',"")

        # main form that can generate the html form
        class HtmlForm(Form):
                # fields all forms will have
            thispage = HiddenField()
            nextpage = HiddenField()
            passdata = HiddenField()
            # turn the form object into an html form
            def get_html(self,action,tableheader):
                # convert a field to just text plus a hidden field for the flags.justhid
                def txt2hid(self,f):
                    new = HiddenField(name=f.name,id=f.id,_form=self)
                    new.process_data(f.data)
                    return (f.data or "")+str(new)
                main_fields = [getattr(self,"button1",None), getattr(self,"button2",None), self.nextpage, self.thispage, self.passdata]
                r = b'''<form method="post" action="''' + bytes(action,'UTF-8') + b'">\n'
                # all the required fields for the top
                r = r + ''.join( [str(f) for f in filter(lambda z: z,main_fields)] + ["\n"] ).encode('utf_8')
                # html table for the other fields
                htable = PrettyTable()
                htable.header = tableheader
                # loop all the remaining fields
                for i in list(filter(lambda z: z not in main_fields,self)):
                    # should be a single FieldList or multiple other fields
                    if type(i) is FieldList:
                    # loop all data row entries
                        for e in i.entries:
                            # if 1st row, populate html table headers w/ labels (except hidden)
                            if tableheader:
                                htable.field_names = [" "*x if d.type == "HiddenField" or d.type == "SubmitField" else str(d.label) for d,x in zip(e,range(32))]
                            # add all fields to html table row (except if flags.justtxt set, just do the fields data)
                            htable.add_row([d.data if d.flags.justtxt else txt2hid(self,d) if d.flags.justhid else str(d) for d in e])
                            # only the first time
                            tableheader = False
                    else:
                        # add a html table row for each field
                        htable.add_row([str(i.label),str(i)])
                r = r + html.unescape(htable.get_html_string()).encode('utf_8')
                r = r + b'''\n</form>'''
                return (r)

        # this classes is the usual base class
        class MainForm(HtmlForm):
            button1 = SubmitField()
            button2 = SubmitField()

        # these 2 classes are used for the main and adv menu
        class SubMenuForm(Form):
            select = SubmitField()
            description = StringField()
        class MenuForm(HtmlForm):
            menu = FieldList(FormField(SubMenuForm))

        # some defaults
        msg = b''
        pagetitle = b''
        tableheader = False
        form = None

        # get the POST query string
        qs = self.get_post_data(environ)
        _logger.debug(str(("Admin method: POST data:",qs)))
        qs.setdefault("nextpage","mainmenu")
        qs.setdefault("thispage","mainmenu")

        try:
            # process the form that was submitted but ignore the -n on some forms like wizard forms
            match qs["thispage"].split("-")[0]:

                case "mainmenu":
                    _logger.debug(str(("Admin method: menu selected.")))
                    # map the clicked button to a form/page to load
                    for f,m in [("Linphone","linmenu"),("Voip.ms","voipmsmenu"),("Wizard","wizmenu-1"),("Advanced","advmenu")]:
                        if f in qs.values():
                            qs["nextpage"] = m

                case "advmenu":
                    _logger.debug(str(("Admin method: menu selected.")))
                    # map the clicked button to a form/page to load
                    for f,m in [("Exit","mainmenu"),
                        ("Enable_OpenSIPS","advmenu-enableopensips"),
                        ("Disable_OpenSIPS","advmenu-disableopensips"),
                        ("Stop_OpenSIPS","advmenu-stopopensips"),
                        ("Adjust_Log_Level","advmenu-setloglevel"),
                        ("Adjust_xLog_Level","advmenu-setxloglevel"),
                        ("Restart_Container","advmenu-restart"),
                        ("Set_Global_DEBUG","advmenu-setglobaldebug"),
                        ("Adjust_MMSGate_Log_Level","advmenu-setmmsgateloglevel"),
                        ("Set_Admin_Password","advmenu-setadminpassword"),
                        ("Display_SQLite_data","advmenu-dumpdatabase"),
                        ("Display_Live_Logs","advmenu-displaylivelogs")]:
                        if f in qs.values():
                            qs["nextpage"] = m

                case "configmenu":
                    if "Cancel" in qs.values():
                        qs["nextpage"] = "voipmsmenu"

                case "wizmenu":
                    try:
                        n = qs["thispage"].split("-")[1]
                        n = int(n)
                    except (ValueError, IndexError):
                        n = 0
                    if "Cancel" in qs.values():
                        qs["nextpage"] = "mainmenu" if n < 2 else "wizmenu-" + str(n-1)
                    if "Next" in qs.values():
                        pass

                case "voipmsmenu":
                    qs["nextpage"] = "voipmsmenu"
                    # check each key for a button pressed
                    for key in list(qs.keys()):
                        if qs[key] == "Apply":
                            usernamekey = key.replace('apply','username')
                            username = qs.get(usernamekey,"")
                            sms_mmskey = key.replace('apply','sms_mms')
                            sms_mms = qs.get(sms_mmskey,"")
                            pushnotificationkey = key.replace('apply','pushnotification')
                            pushnotification = qs.get(pushnotificationkey,"")
                            qs["tmpdata"] = (username,sms_mms,pushnotification)
                            qs["thispage"] = "subacctmenu"
                            break
                        if qs[key] == "Refresh":
                            # make it look like we just opened page for first time
                            qs["thispage"] = "mainmenu"
                            break
                        if qs[key] == "Client Config":
                            usernamekey = key.replace('client_config','username')
                            username = qs.get(usernamekey,"")
                            qs["config-0-ha1"] = "y"
                            qs["config-0-username"] = username
                            qs["nextpage"] = "configmenu"
                        if qs[key] == "Cancel":
                            # go back to main menu
                            qs["nextpage"] = "mainmenu"
                            break

                case "linmenu":
                    qs["nextpage"] = "linmenu"
                    # check each key for a button pressed
                    for key in list(qs.keys()):
                        if qs[key] == "Enter code":
                            usernamekey = key.replace('action','username')
                            username = qs.get(usernamekey,"")
                            verifykey = key.replace('action','entry')
                            verify = qs.get(verifykey,"")
                            qs["thispage"] = "verifymenu"
                            qs["tmpdata"] = (username,verify)
                        if qs[key] == "Set email":
                            usernamekey = key.replace('action','username')
                            username = qs.get(usernamekey,"")
                            emailkey = key.replace('action','entry')
                            email = qs.get(emailkey,"")
                            qs["thispage"] = "emailmenu"
                            qs["tmpdata"] = (username,email)
                        if qs[key] == "Solved":
                            usernamekey = key.replace('action','username')
                            username = qs.get(usernamekey,"")
                            qs["thispage"] = "solvedmenu"
                            qs["tmpdata"] = username
                            break
                        if qs[key] == "Set Password":
                            usernamekey = key.replace('action','username')
                            username = qs.get(usernamekey,"")
                            passwordkey = key.replace('action','entry')
                            password = qs.get(passwordkey,"")
                            qs["thispage"] = "updatemenu"
                            qs["tmpdata"] = (username,password)
                            break
                        if qs[key] == "Delete":
                            # display the yes/no prompt page
                            qs["nextpage"] = "deletemenu"
                            usernamekey = key.replace('delete','username')
                            qs["passdata"] = qs.get(usernamekey,"")
                            msg = ("Delete the linphone account named " + qs[usernamekey] + " from the database?").encode('utf_8')
                            pagetitle = b' - Manage Linphone Accounts'
                            break
                        if qs[key] == "Cancel":
                            # go back to main menu
                            qs["nextpage"] = "mainmenu"
                            break
                        if qs[key] == "Add":
                            usernamekey = key.replace('action','linacct')
                            username = qs.get(usernamekey,"")
                            try:
                        # check valid name
                                if not all(char in string.ascii_letters+string.digits+"!#$%&'*+-/=?^_`{|}~" for char in username) or username == '':
                                    raise Exception("Bad user name.")
                                rslt = requests.get("https://subscribe.linphone.org/api/accounts/"+username+"@sip.linphone.org/info",
                                  headers={"Content-Type":"application/json","accept":"application/json"})
                                # if exists
                                if is_json(rslt.text) and "activated" in rslt.json():
                                    qs["thispage"] = "addmenu"
                                    qs["tmpdata"] = (username,None,None,None)
                                # acct does NOT exist
                                else:
                                    # get account_creation_request_token
                                    rslt = requests.post("https://subscribe.linphone.org/api/account_creation_request_tokens",
                                      headers={"Content-Type":"application/json","accept":"application/json"})
                                    if is_json(rslt.text) and "token" in rslt.json():
                                        rsltj = rslt.json()
                                        _logger.debug(str(("Admin method: Get token:",rsltj)))
                                        # random password
                                        alphabet = string.ascii_letters + string.digits + "_!#-="
                                        password = ''.join(secrets.choice(alphabet) for i in range(8))
                                        qs["thispage"] = "addmenu"
                                        qs["tmpdata"] = (username,password,rsltj["token"],rsltj["validation_url"])
                                    else:
                                        _logger.error(str(("Admin error getting token:",rslt.status_code,rslt.text)))
                                        msg = ("Admin error getting token: ("+str(rslt.status_code)+") "+rslt.text).encode('utf_8')
                            except Exception as e:
                                msg = ('Add Linphone account failed: '+str(e)).encode('utf_8')
                                _logger.error(str(("Admin linacct Add error:",e)))
                            break
                case _:
                    _logger.error(str(("Admin error: default for thispage")))

        except Exception as e:
            PrintException(e)
            _logger.error(str(("Admin wizard submitted form:",e)))

        try:
            # generate next form but ignore the -n on some forms like wizard forms
            match qs["nextpage"].split("-")[0]:

                case "mainmenu":
                    qs["thispage"] = "mainmenu"
                    qs["nextpage"] = "mainmenu"

                    form = MenuForm(data=qs)
                    # generate the main menu
                    for b,d in [("Wizard","Step by step configuration of MMSGate"),
                        ("Linphone","Manage Linphone accounts for push notifications - Add/Edit/Delete"),
                        ("Voip.ms","Configure Voip.ms Accounts for MMSGate and configure clients"),
                        ("Advanced","Advanced menu for logs, restarts, etc.")]:
                        f = form.menu.append_entry({"select":b,"description":d})
                        f["description"].flags.justtxt = True
                        f["select"].label.text = b

                # display the advanced menu and operations
                case "advmenu":
                    import uuid
                    qs["thispage"] = "advmenu"
                    pagetitle = "- Advanced".encode('utf_8')
                    # return to adv menu from option?
                    if qs.get('button1',"") == 'Return':
                        qs["nextpage"] = "advmenu"
                    # default to menu
                    AdvForm = MenuForm
                    # sub operations in advanced menu
                    match (qs["nextpage"]+"-").split("-")[1]:
                        # disable opensips?
                        case "disableopensips":
                            e = get_global("ENABLEOPENSIPS")
                            if e == "N":
                                msg += b'OpenSIPS is already disabled.  If stopped, it will not restart.<br>'
                            else:
                                set_global("ENABLEOPENSIPS", "N")
                                msg += b'OpenSIPS has been disabled.  If stopped, it will not restart.<br>'
                        # enable opensips?
                        case "enableopensips":
                            e = get_global("ENABLEOPENSIPS")
                            if e == "Y":
                                msg += b'OpenSIPS is already enabled.<br>'
                            else:
                                set_global("ENABLEOPENSIPS", "Y")
                                msg += b'OpenSIPS has been enabled.  If currentelly stopped, it will start within a minute.<br>'
                        # stop opensips
                        case "stopopensips":
                            os.system("sudo /scripts/stopopensips.sh")
                            msg += b'OpenSIPS has been stopped.<br>'
                        # set Log level?
                        case "setloglevel":
                            # hit the apply button?
                            if qs.get('Apply',"") == 'Apply':
                                newlvl = qs.get("loglevel","")
                                r = subprocess.run(["opensips-cli","-x","mi","log_level",newlvl],capture_output=True)
                                _logger.debug(str(("opensips-cli -x mi log_level "+newlvl,r)))
                                if r.returncode == 0:
                                    msg += b'New log level applied successfully.<br>'
                                else:
                                    msg += ('Error: The "opensips-cli mi log_level" command return '+str(r.returncode)+'.<br>').encode('utf_8')
                            # the xLog dialog
                            class AdvForm(MainForm):
                                loglevel = SelectField("Log Level",choices=[("-3","Alert level"),
                                  ("-2","Critical level"),
                                  ("-1","Error level"),
                                  ("1","Warning level"),
                                  ("2","Notice level"),
                                  ("3","Info level"),
                                  ("4","Debug level")])
                                Apply = SubmitField()
                            r = subprocess.run(["opensips-cli","-x","mi","log_level"],capture_output=True)
                            _logger.debug(str(("opensips-cli -x mi log_level",r)))
                            try:
                                j = json.loads(r.stdout)
                                qs["loglevel"]=str(j["Processes"][0]["Log level"])
                            except Exception as e:
                                msg += ('Error: The "opensips-cli mi log_level" command return '+str(r.returncode)+'.<br>').encode('utf_8')
                            qs["nextpage"] = "advmenu-setloglevel"

                        case "dumpdatabase":
                            # simple sqlite3 command w/ -html easy way to dump to web page
                            sp = subprocess.run(["bash","-c",". /etc/opensips/globalcfg.sh; sqlite3 -html -header  $DBPATHM \""+
                              "SELECT rowid,msgid,strftime('%Y-%m-%d %H:%M',datetime(rcvd_ts, 'unixepoch', 'localtime')) as rcvd_ts, "+
                                "strftime('%Y-%m-%d %H:%M',datetime(sent_ts, 'unixepoch', 'localtime')) as sent_ts,fromid,fromdom,toid,todom, "+
                                "substr(message,1,30) as message,direction as dir,msgstatus as msgstat,did,msgtype,trycnt FROM send_msgs order by rowid;\""],capture_output=True)
                            _logger.debug(str(("db dump send_msgs ret:",sp)))
                            msg += b'Database dump of table "send_msgs":<br><table>' + sp.stdout + b'</table><br>'
                            sp = subprocess.run(["bash","-c",". /etc/opensips/globalcfg.sh; sqlite3 -html -header  $DBPATHM \""+
                              "SELECT rowid,* FROM subacct order by rowid;\""],capture_output=True)
                            _logger.debug(str(("db dump subacct ret:",sp)))
                            msg += b'Database dump of table "subacct":<br><table>' + sp.stdout + b'</table><br>'
                            sp = subprocess.run(["bash","-c",". /etc/opensips/globalcfg.sh; sqlite3 -html -header  $DBPATHM \""+
                              "SELECT rowid,* FROM linphone order by rowid;\""],capture_output=True)
                            _logger.debug(str(("db dump linphone ret:",sp)))
                            msg += b'Database dump of table "linphone":<br><table>' + sp.stdout + b'</table><br>'
                            sp = subprocess.run(["bash","-c",". /etc/opensips/globalcfg.sh; sqlite3 -html -header  $DBPATH \""+
                              "SELECT id, src_addr, dst_addr, username, domain, "+
                              "strftime('%Y-%m-%d %H:%M',datetime(inc_time, 'unixepoch', 'localtime')) as inc_time, "+
                              "strftime('%Y-%m-%d %H:%M',datetime(exp_time, 'unixepoch', 'localtime')) as exp_time, "+
                              "strftime('%Y-%m-%d %H:%M',datetime(snd_time, 'unixepoch', 'localtime')) as snd_time, ctype, substr(body,1,30) as body FROM silo order by rowid;\""],capture_output=True)
                            _logger.debug(str(("db dump silo ret:",sp)))
                            msg += b'Database dump of OpenSIPS table "silo":<br><table>' + sp.stdout + b'</table><br>'

                        case "setglobaldebug":
                            # get current value
                            gdbg = get_global("DEBUG")
                            _logger.debug(str(("Current global debug:",gdbg)))
                            # hit the apply button?
                            if qs.get('Apply','') == 'Apply':
                                # new value
                                newgdbg = qs.get("globaldebug",'N').upper()
                                _logger.debug(str(("Set global debug to:",newgdbg)))
                                # no change?
                                if newgdbg == gdbg:
                                    msg += ('Global DEBUG already set to"'+newgdbg+'". No changes done.<br>').encode('utf_8')
                                else:
                                    # update it!
                                    set_global("DEBUG",newgdbg)
                                    gdbg = newgdbg
                                    msg += ('Global DEBUG set to"'+newgdbg+'" successfully.<br>').encode('utf_8')
                            # value forform
                            qs["globaldebug"] = False if gdbg == 'N' else True
                            # the form
                            class AdvForm(MainForm):
                                globaldebug = BooleanField("Global DEBUG for MMSGate BASH scripts.")
                                Apply = SubmitField()
                            # return here after submit
                            qs["nextpage"] = "advmenu-setglobaldebug"

                        case "restart":
                            if qs.get('Apply',"") == 'Apply':
                                if qs.get("restart",'n') == 'y':
                                    msg += b'Container restarting.  Please wait 10 seconds and click Return.<br>'
                                    subprocess.Popen(['sudo','/scripts/restart.sh'])
                                else:
                                    msg += b'To restart container, check the box above and click Apply.<br>'
                            class AdvForm(MainForm):
                                restart = BooleanField("Check to confirm restart and click Apply.")
                                Apply = SubmitField()
                            qs["nextpage"] = "advmenu-restart"

                        case "setadminpassword":
                            if qs.get('Apply',"") == 'Apply':
                                password1 = qs.get("adminpw",'')
                                password2 = qs.get("readminpw",'')
                                if password1 == '':
                                    msg += b'Password cannot be blank.<br>'
                                elif password1 == password2:
                                    r = os.system('echo -n "admin:" > /etc/opensips/nginx/.htpasswd && openssl passwd -apr1 "'+password1+'" >> /etc/opensips/nginx/.htpasswd')
                                    if r == 0:
                                        msg += b'Password successfully changed.<br>'
                                    else:
                                        msg += b'Failed to change password.<br>'
                                else:
                                    msg += b'Password and confirmed password do not match.<br>'
                            class AdvForm(MainForm):
                                adminpw = PasswordField("Enter new admin password.")
                                readminpw = PasswordField("Re-enter new admin password to confirm.")
                                Apply = SubmitField()
                            qs["nextpage"] = "advmenu-setadminpassword"

                        case "setmmsgateloglevel":
                            curlvlstr = "WARNING"
                            self.ask_q.put(("GetLogLevel",))
                            curlvl = self.loglvl_q.get()
                            for lvl in cfg.loglevels.keys():
                                if curlvl == cfg.loglevels[lvl]:
                                    curlvlstr = lvl
                            try:
                                # hit the apply button?
                                if qs.get('Apply',"") == 'Apply':
                                    newlvlstr = qs.get("mmsgateloglevel","")
                                    newlvl = cfg.loglevels[newlvlstr]
                                    _logger.setLevel(newlvl)
                                    self.ask_q.put(("SetLogLevel",newlvl))
                                    curlvlstr = newlvlstr
                                    msg += b'MMSGate log level has been modified.<br>'
                            except Exception as e:
                                _logger.error(str(("Error: Bad MMSGate log level:",e)))
                                msg += b'Error asjusting MMSGate log level.<br>'
                                newlvlstr = curlvlstr
                            qs["mmsgateloglevel"] = curlvlstr

                            class AdvForm(MainForm):
                                mmsgateloglevel = SelectField("MMSGate Log Level",choices=[("DEBUG","Debug"),
                                  ("INFO","Information"),
                                  ("WARNING","Warning - default"),
                                  ("ERROR","Error"),
                                  ("CRITICAL","Critical")])
                                Apply = SubmitField()

                            qs["nextpage"] = "advmenu-setmmsgateloglevel"

                        # set xLog level?
                        case "setxloglevel":
                            # hit the apply button?
                            if qs.get('Apply',"") == 'Apply':
                                newlvl = qs.get("xloglevel","")
                                r = subprocess.run(["opensips-cli","-x","mi","xlog_level",newlvl],capture_output=True)
                                _logger.debug(str(("opensips-cli -x mi xlog_level "+newlvl,r)))
                                if r.returncode == 0:
                                    msg += b'New xlog level applied successfully.<br>'
                                else:
                                    msg += ('Error: The "opensips-cli mi xlog_level" command return '+str(r.returncode)+'.<br>').encode('utf_8')
                            # the xLog dialog
                            class AdvForm(MainForm):
                                xloglevel = SelectField("xLog Level",choices=[("-3","Alert level"),
                                  ("-2","Critical level"),
                                  ("-1","Error level"),
                                  ("1","Warning level"),
                                  ("2","Notice level"),
                                  ("3","Info level"),
                                  ("4","Debug level")])
                                Apply = SubmitField()
                            r = subprocess.run(["opensips-cli","-x","mi","xlog_level"],capture_output=True)
                            _logger.debug(str(("opensips-cli -x mi xlog_level",r)))
                            try:
                                j = json.loads(r.stdout)
                                qs["xloglevel"]=str(j["xLog Level"])
                            except Exception as e:
                                msg += ('Error: The "opensips-cli mi xlog_level" command return '+str(r.returncode)+str(e)+'.<br>').encode('utf_8')
                            qs["nextpage"] = "advmenu-setxloglevel"
                        case "displaylivelogs":
                            # hit the apply button?
                            if qs.get('Apply','') == 'Apply':
                                pass
                            # the display logs dialog
                            class AdvForm(MainForm):
                                DisplayLog = SelectField("Display Log",choices=[("/var/log/mmsgate.log","mmsgate.log"),
                                  ("/var/log/opensips.log","opensips.log"),
                                  ("/var/log/gunicorn.log","gunicorn.log"),
                                  ("/var/log/nginx/access.log","Nginx access.log"),
                                  ("/var/log/nginx/error.log","Nginx error.log"),
                                  ("/var/log/syslog","syslog")])
                                IncludeLastLines = SelectField("Include Last Lines",choices=[('100','100'),('500','500'),('1000','1000')])
                                LiveLogDuration = SelectField("Live Log Duration (minutes)",choices=[('1','1'),('5','5'),('10','10'),('20','20'),('30','30'),('60','60')])
                                Apply = SubmitField()
                            # get the values from qs or defaults placed in qs
                            try:
                                minutes = int(qs['LiveLogDuration'])
                            except ValueError:
                                minutes = 5
                                qs['LiveLogDuration'] = str(minutes)
                            lines = qs.get('IncludeLastLines','100')
                            log = qs.get('DisplayLog','/var/log/mmsgate.log')

                            # put the fifo name in the form for the javascript
                            qs['passdata'] = "log-" + str(uuid.uuid4())
                            # create the fifo
                            os.makedirs(self.fifodir,exist_ok=True)
                            fname = self.fifodir + '/' + qs['passdata']
                            os.mkfifo(fname, mode=0o777)
                            f = os.open(fname, os.O_RDONLY | os.O_NONBLOCK)
                            # use tail to put recent/new log entries into the fifo.  limit 5 minutes.  remove fifo when done.
                            fout = os.open(fname, os.O_WRONLY)
                            cmd = "sudo /scripts/displaylog.sh {} {} {} {}".format(fname,log,minutes*60,lines)
                            _logger.debug(str(("live log fifo cmd:",cmd)))
                            # javascript for updating client w/ recent/new log entries
                            jscript = b'''<script>
// check for recent/new log entries every 2 seconds
myInterval = setInterval(getlogFunction, 2000);
// get first chunck
getlogFunction();
// call this from interval timer
function getlogFunction () {
    // REST call for log data
    var log = new XMLHttpRequest();
    log.onreadystatechange = function() {
      if (this.readyState == 4 && this.status == 200) {
        e = document.getElementById("log");
        // 0 is first load
        l = e.value.length;
        // true is currentelly scrolled to bottom
        b = Math.abs(e.scrollHeight - e.clientHeight - e.scrollTop) <= 1;
        if (this.responseText == "--DONE--") {
          // limit reached
          e.innerHTML += "Reached max ''' + str(minutes).encode('utf_8') + b''' minutes!";
          clearInterval(myInterval);
          setCaretToPos(e,e.value.length);
        } else {
          // append log entries
          e.innerHTML += this.responseText;
        }
        // was first fill or prev scrolled to bottom?
        if (l == 0 || b) {
          // scroll to bottom
          e.scrollTop = e.scrollHeight;
        }
      }
    };
    // path to get log entries.  fifo name in url path
    url = "/admin?log=" + document.getElementById("passdata").value;
    // send http query
    log.open("GET", url, false);
    log.send();
}
function setCaretToPos(input, pos) {
    if (input.setSelectionRange) {
        input.setSelectionRange(pos, pos);
    } else if (input.createTextRange) {
        const range = input.createTextRange();
        range.collapse(true);
        range.moveEnd('character', pos);
        range.moveStart('character', pos);
        range.select();
    }
}
</script>'''
                            if lynx:
                                msg += b'Please use log viewing console commands.  Lynx is not compatible with live logs.<br>'
                            else:
                                # run cmd in detached process for later reading via /admin?log= from javascript
                                process = subprocess.Popen(cmd, bufsize=0, stdout=fout, shell=True)
                                # html textarea will contain log
                                msg += b'Log limited to ' + str(minutes).encode('utf_8') + b' minutes.  Maximun is 1 hour.  '
                                msg += b'For longer, please use log viewing console commands.<br><textarea id="log" readonly style="height:1000px;width:100%"></textarea>' + jscript

                    # build the form as defined or the default
                    form = AdvForm(data=qs)
                    # if it was the default, build menu
                    if AdvForm == MenuForm:
                        # generate the advanced main menu
                        for b,d in [("Enable_OpenSIPS","To allow OpenSIPS to start within 1 minute."),
                            ("Disable_OpenSIPS","Prevent OpenSIPS from restarting if stopped."),
                            ("Stop_OpenSIPS","Stop OpenSIPS and if not disabled, restart it."),
                            ("Adjust_Log_Level","Change the OpenSIPS script log level written to log file."),
                            ("Adjust_xLog_Level","Change the OpenSIPS script xlog level written to log file."),
                            ("Adjust_MMSGate_Log_Level","Change the MMSGate script log level written to log file."),
                            ("Set_Global_DEBUG","Change the Global DEBUG settings used by the system's BASH scripts."),
                            ("Restart_Container","Stop this container and if configured as such in Docker, restart it."),
                            ("Display_Live_Logs","Display live logs in real time.  (Not compatible with Lynx.)"),
                            ("Display_SQLite_data","Dump the database tables used for MMSGate."),
                            ("Set_Admin_Password","Set the admin password for MMSGate."),
                            ("Exit","Return to main menu")]:
                            f = form.menu.append_entry({"select":b,"description":d})
                            f["description"].flags.justtxt = True
                            f["select"].label.text = b
                    else:
                        # did a custom form w/ just a return to main advmenu
                        form.button1.label.text = "Return"
                        del form.button2

                case "configmenu":
                    import copy
                    import hashlib
                    import urllib.parse
                    import uuid
                    import xml.etree.ElementTree as ET
                    from pathlib import Path
                    import qrcode
                    # namespace for client config xml
                    ET.register_namespace("", "http://www.linphone.org/xsds/lpconfig.xsd")
                    # we will come back here if param changed
                    qs["thispage"] = "configmenu"
                    qs["nextpage"] = "configmenu"
                    # get needed data
                    apiid = get_global("APIID")
                    apipw = get_global("APIPW")
                    dnsname = get_global("DNSNAME")
                    # path to put files
                    destdir = cfg.get("web","localmedia")+"/"
                    # url path to get files
                    webpath = cfg.get("web","protocol")+"://"+dnsname+":"+str(cfg.get("web","webport"))+cfg.get("web","pathget")+"/"
                    # open the db
                    conn = get_dbconn()
                    if conn is None:
                        raise Exception("MMSGate db connection failed.")
                    pagetitle = "- Client Config".encode('utf_8')
                    account = qs["config-0-username"]
                    # get 1st account details
                    accts = []
                    linacct = None
                    try:
                        indx = 0
                        acct = {}
                        lacct = {}
                        acct["acct"] = account
                        acct["apw"],acct["cid"],auuid,lacct["acct"],lacct["apw"],lacct["dom"],acct["regexp"] = conn.execute("SELECT s.password, callerid, uuid, username, l.password, l.domain, max_expiry "+
                          "FROM subacct s LEFT JOIN linphone l ON linphone = username "+
                          "WHERE account=?;",(account,)).fetchone()
                        # new unique ref for account config
                        acct["newref"] = ''.join(secrets.choice(string.ascii_letters + string.digits) for i in range(12))
                        accts += [acct]
                        if lacct["acct"] is not None:
                            indx += 1
                            linacct = lacct["acct"]
                            accts += [lacct]
                            acct = {}
                            for acct["acct"], acct["apw"], acct["cid"], acct["regexp"] in conn.execute("SELECT account, password, callerid, max_expiry FROM subacct "+
                              "WHERE tls != 0 AND ext != '' AND account != ? AND linphone = ? AND callerid IS NOT NULL;",(account,linacct)).fetchall():
                                accts += [acct]
                                acct = {}
                        else:
                            indx += 1
                            acct = {}
                            while qs.get("config-"+str(indx)+"-username","") != "":
                                account = qs.get("config-"+str(indx)+"-username","")
                                acct["acct"], acct["apw"], acct["cid"], acct["regexp"] = conn.execute("SELECT account, password, callerid, max_expiry FROM subacct "+
                                  "WHERE ext != '' AND account == ? AND callerid IS NOT NULL;",(account,)).fetchone()
                                accts += [acct]
                                indx += 1
                                acct = {}
                        _logger.debug(str(("configmenu: Done init accts[]",accts)))
                    except Exception as e:
                        PrintException(e)
                        msg += ("Error getting account info: "+str(e)+"<br>").encode('utf_8')
                    msg += b'<br>'
                    # need a unique uuid for each subacct to store config.
                    if (auuid is None):
                        auuid = str(uuid.uuid4())
                        # remember it
                        conn.execute("UPDATE subacct SET uuid = ? WHERE account=?;",(auuid,account))
                        conn.commit()
                    # build paths
                    acctdestdir = destdir + auuid + "/"
                    acctwebpath = webpath + auuid + "/"
                    Path(acctdestdir).mkdir(parents=True, exist_ok=True)
                    # get localpaths and urls
                    acctdestfile = acctdestdir + account + ".xml"
                    acctwebfile = acctwebpath + account + ".xml"
                    acctdestfileqr = acctdestdir + account + "-cfg.png"
                    acctwebfileqr = acctwebpath + account + "-cfg.png"
                    fileuploadpath = cfg.get("web","protocol")+"://"+dnsname+":"+str(cfg.get("web","webport"))+cfg.get("web","pathfile")
                    # cache the data
                    if 'ciddids' not in vars():
                        ciddids = {}
                    if 'popdids' not in vars():
                        popdids = {}
                    indx = 0
                    # get DID host and other info for the sub accounts.
                    try:
                        for acct in accts:
                            ciddid = acct.get("cid",None)
                            if ciddid is not None:
                                if ciddid not in ciddids:
                                    url="https://voip.ms/api/v1/rest.php?api_username={}&api_password={}&method=getDIDsInfo&did={}"
                                    if apiid == "" or apipw == "":
                                        raise RuntimeError("apiid or apipw not configured.")
                                    r = requests.get(url.format(apiid,urllib.parse.quote(apipw),ciddid))
                                    _logger.debug(str(("REST getDIDsInfo ret:",str(r.text))))
                                    if is_json(r.text):
                                        rslt = r.json()
                                        if rslt["status"] == "success":
                                            for did in rslt["dids"]:
                                                ciddids[did["did"]] = did
                                        else:
                                            raise RuntimeError(rslt["status"])
                                    else:
                                        raise RuntimeError("Data returned from Voip.ms API not in JSON format. "+str((r.txt)))
                                acct["pop"] = ciddids[ciddid]["pop"]
                                acct["description"] = ciddids[ciddid]["description"]
                                if acct["pop"] not in popdids:
                                    # get the server info the the DID's PoP.  need the host name.
                                    url="https://voip.ms/api/v1/rest.php?api_username={}&api_password={}&method=getServersInfo&server_pop={}"
                                    if apiid == "" or apipw == "":
                                        raise RuntimeError("apiid or apipw not configured.")
                                    r = requests.get(url.format(apiid,urllib.parse.quote(apipw),acct["pop"]))
                                    _logger.debug(str(("REST getServersInfo ret:",str(r.text))))
                                    if is_json(r.text):
                                        rslt = r.json()
                                        if rslt["status"] == "success":
                                            for srv in rslt["servers"]:
                                                popdids[srv["server_pop"]] = srv
                                        else:
                                            raise RuntimeError(rslt["status"])
                                    else:
                                        raise RuntimeError("Data returned from Voip.ms API not in JSON format. "+str((r.txt)))
                                acct["dom"] = popdids[acct["pop"]]["server_hostname"]
                                acct["popname"] = popdids[acct["pop"]]["server_name"]
                            # include ha1 in xml config file?
                            if "config-"+str(indx)+"-username" not in qs or qs.get("config-"+str(indx)+"-ha1","n") == "y":
                              m = hashlib.md5()
                              m.update((acct["acct"]+":"+acct["dom"]+":"+acct["apw"]).encode('utf-8'))
                              acct["ha1"] = m.hexdigest()
                            indx += 1
                        _logger.debug(str(("configmenu: Done populate accts[]",accts)))
                    except Exception as e:
                        PrintException(e)
                        msg += ("Error: "+str(e)+"<br>").encode('utf_8')
                    # make fresh contacts vcard
                    vf = open(acctdestdir+"contacts.vcard","w+")
                    for acct, cid, ext, icnam, desc in conn.execute("SELECT account, callerid, ext, internal_cnam, description FROM subacct WHERE ext != '' ORDER BY account").fetchall():
                        if (desc == ""):
                            if (icnam == ""):
                                fn = ext
                            else:
                                fn = icnam
                        else:
                            fn = desc
                        vf.write("BEGIN:VCARD\nVERSION:4.0\nKIND:individual\nIMPP:{0}\nFN:{1}\nEND:VCARD\n".format("sips:"+ext+"@"+accts[0]["dom"],fn))
                    # get list of avail accts for form choices later.
                    avail_accts = conn.execute("SELECT account, account FROM subacct "+
                      "WHERE callerid IS NOT NULL AND tls != 0 AND ext != '' AND linphone IS NULL AND account NOT IN (" + ', '.join(["'"+a["acct"]+"'" for a in accts]) + ") ORDER BY account").fetchall()
                    vf.close()
                    conn.close()
                    msg += ("Generated vcard contacts list for including in XML config: <a href='" + acctwebpath + "contacts.vcard'>contacts.vcard</a><br>" ).encode('utf_8')
                    # these 2 classes are for client config form
                    class SubConfigForm(Form):
                        username = SelectField("SIP Account")
                        domain = StringField("Domian/Server")
                        ha1 = BooleanField("Include encrypted password")
                        callerid = StringField("DID/CallerID")
                        diddesc = StringField("DID description")
                        popname = StringField("PoP name")
                    class ConfigForm(MainForm):
                        config = FieldList(FormField(SubConfigForm))
                    # setup form
                    form = ConfigForm(data=qs)
                    form.button1.label.text = "Refresh"
                    form.button2.label.text = "Cancel"
                    tableheader = True
                    # populate rows in form
                    for acct in accts:
                        # populate rows 
                        f = form.config.append_entry({"username":acct["acct"], "domain":acct["dom"], "ha1":"y" if "ha1" in acct else None, 
                          "callerid":acct.get("cid",""), "diddesc":acct.get("description",""), "popname":acct.get("popname","")})
                        # it's a select field, need the choice tomatch the data
                        f["username"].choices=[(acct["acct"],acct["acct"])]
                        # 
                        for fld in ["username","domain","callerid","diddesc","popname"]:
                            f[fld].flags.justhid = True
                    if linacct is None:
                        f = form.config.append_entry({"domain":"","ha1":"y","callerid":"", "diddesc":"", "popname":""})
                        f["username"].choices=[("","")]+avail_accts
                        for fld in ["domain","callerid","diddesc","popname"]:
                            f[fld].flags.justhid = True
                        
                    # build the xml config file
                    root = ET.fromstring('<config xmlns="http://www.linphone.org/xsds/lpconfig.xsd" '+
                        'xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:schemaLocation="http://www.linphone.org/xsds/lpconfig.xsd lpconfig.xsd"></config>')
                    # new ref for nat_policy
                    newref = ''.join(secrets.choice(string.ascii_letters + string.digits) for i in range(12))
                    # get generic config for a linphone acct
                    rslt = requests.get("https://subscribe.linphone.org/provisioning")
                    if rslt.status_code == 200:
                        oldroot = ET.fromstring(rslt.text)
                        # clean up xml and copy over to root, remove dup sections and unwanted settings
                        for child in oldroot:
                            newchild = root.find("*[@name='"+child.attrib["name"]+"']")
                            if newchild is None:
                                newchild = root.makeelement("section",{"name":child.attrib["name"]})
                                root.append(newchild)
                            for gchild in child:
                                if gchild.attrib["name"] not in ["quality_reporting_collector","conference_factory_uri","audio_video_conference_factory_uri","lime_server_url","rls_uri"]:
                                    newgchild = copy.deepcopy(gchild)
                                    newchild.append(newgchild)
                        # for generic, need a few extra elements for auth
                        auth = root.makeelement("section",{"name":"auth_info_0"})
                        for n,v in [("username",""),("domain",""),("realm",""),("ha1",""),("algorithm","MD5")]:
                            e = root.makeelement("entry",{"name":n,"overwrite":"true"})
                            e.text = v
                            auth.append(e)
                        root.append(auth)
                        # edit it and save account config (proxy and auth_info) for later
                        for child in root:
                            if child.attrib["name"] == "proxy_0":
                                # generic config, need a few extra elems
                                for n in ["reg_proxy","reg_route","realm","nat_policy_ref"]:
                                    e = root.makeelement("entry",{"name":n,"overwrite":"true"})
                                    child.append(e)
                                for e in child.findall("*[@name='quality_reporting_enabled']"):
                                    e.text = "0"
                                for e in child.findall("*[@name='publish']"):
                                    e.text = "0"
                                for e in child.findall("*[@name='reg_proxy']"):
                                    e.text = "<sip:" + dnsname + ";transport=tls>"
                                for e in child.findall("*[@name='reg_route']"):
                                    e.text = "<sip:" + dnsname + ";transport=tls>"
                                for e in child.findall("*[@name='nat_policy_ref']"):
                                    e.text = newref
                                originalproxy = copy.deepcopy(child)
                                root.remove(child)
                            if child.attrib["name"] == "auth_info_0":
                                originalauthinfo = copy.deepcopy(child)
                                root.remove(child)
                            if child.attrib["name"] == "nat_policy_0":
                                for e in child.findall("*[@name='stun_server']"):
                                    e.text = "stun.linphone.org"
                            if child.attrib["name"] == "misc":
                                for e in child.findall("*[@name='contacts-vcard-list']"):
                                    e.text = acctwebpath + "contacts.vcard"
                                e = child.makeelement("entry",{"name":"hide_chat_rooms_from_removed_proxies","overwrite":"true"})
                                e.text = "0"
                                child.append(e)
                                for e in child.findall("*[@name='file_transfer_server_url']"):
                                    e.text = fileuploadpath
                                for e in child.findall("*[@name='log_collection_upload_server_url']"):
                                    e.text = fileuploadpath
                                # config url for loading xml at each start... including xml causes issues w/ default_proxy
                                for e in child.findall("*[@name='config-uri']"):
                                    e.text = ""
                            if child.attrib["name"] == "sip":
                                for e in child.findall("*[@name='media_encryption']"):
                                    e.text = "srtp"
                                for e in child.findall("*[@name='media_encryption_mandatory']"):
                                    e.text = "1"
                                e = child.makeelement("entry",{"name":"im_notif_policy","overwrite":"true"})
                                e.text = "none"
                                child.append(e)
                                e = child.makeelement("entry",{"name":"default_proxy","overwrite":"true"})
                                e.text = "0"
                                child.append(e)
                                # no IPv6
                                e = child.makeelement("entry",{"name":"use_ipv6","overwrite":"true"})
                                e.text = "0"
                                child.append(e)
                            if child.attrib["name"] == "app":
                                e = child.makeelement("entry",{"name":"publish_presence","overwrite":"true"})
                                e.text = "0"
                                child.append(e)
                        # add auth_indo and proxy back in for each account 
                        indx = 0
                        for acct in accts:
                            newauthinfo = copy.deepcopy(originalauthinfo)
                            newauthinfo.set("name","auth_info_"+str(indx))
                            for e in newauthinfo.findall("*[@name='username']"):
                                e.text = acct["acct"]
                            for e in newauthinfo.findall("*[@name='domain']"):
                                e.text = acct["dom"]
                            for e in newauthinfo.findall("*[@name='realm']"):
                                e.text = acct["dom"]
                            for e in newauthinfo.findall("*[@name='ha1']"):
                                e.text = acct.get("ha1","")
                            root.append(newauthinfo)
                            newproxy = copy.deepcopy(originalproxy)
                            newproxy.set("name","proxy_"+str(indx))
                            for e in newproxy.findall("*[@name='reg_identity']"):
                                if acct["dom"] == "sip.linphone.org":
                                    e.text = '"' + acct["acct"] + '" <sip:' + acct["acct"] + '@' + acct["dom"] + ';transport=tls>'
                                else:
                                    e.text = '"' + acct["acct"] + '" <sips:' + acct["acct"] + '@' + acct["dom"] + '>'
                            for e in newproxy.findall("*[@name='realm']"):
                                e.text = acct["dom"]
                            if acct.get("regexp",0) != 0:
                                for e in newproxy.findall("*[@name='reg_expires']"):
                                    e.text = acct["regexp"]
                            root.append(newproxy)
                            indx += 1
                        msg += ("Built XML config for accounts: " + ', '.join([a["acct"] for a in accts]) + "<br>").encode('utf_8')
                        # save config file w/ all accounts
                        tree = ET.ElementTree(root)
                        tree.write(acctdestfile)
                        # make qr code 
                        img = qrcode.make(acctwebfile)
                        img.save(acctdestfileqr)
                        msg += ("XML Config URL: <a href='"+acctwebfile+"' target='_blank'>"+acctwebfile+"</a><br>").encode('utf_8')
                        msg += ("QR Code Config URL: <a href='"+acctwebfileqr+"' target='_blank'>"+acctwebfileqr+"</a><br>").encode('utf_8')
                        msg += ("<img src='"+acctwebfileqr+"' width='33%' height='33%' /><br>").encode('utf_8')
                    else:
                        msg += ("Error: Bad return code from https://subscribe.linphone.org/provisioning: "+str(rslt.status_code)+"<br>").encode('utf_8')
                case "wizmenu":
                    try:
                        n = qs["nextpage"].split("-")[1]
                        n = int(n)
                    except (ValueError, IndexError):
                        n = 0
                    pagetitle = "- Wizard".encode('utf_8')
                    qs["thispage"] = "wizmenu-" + str(n)
                    qs["nextpage"] = "wizmenu-" + str(n+1)
                    WizForm = MainForm
                    match n:
                        # intro for the wizard
                        case 1:
                            msg += ("This is the MMSGate Wizard.  It will help you configure the Docker container and local network.  Select Cancel at any time to return to the previous page.  " +
                              "Click \"Next\" to continue.  This will stop OpenSIPS and disable it." ).encode('utf_8')
                        # disable and stop opensips and config router/firewall
                        case 2:
                            set_global("ENABLEOPENSIPS", "N")
                            os.system("sudo /scripts/stopopensips.sh")
                            ipaddr=get_ip()
                            _logger.debug(str(("get_ip() ret:",ipaddr)))
                            class WizForm(MainForm):
                                IPv4_external_address = StringField("IPv4 External Address",render_kw={'readonly':''})
                            msg += (b"WARNING: MMSGate cannot determine the host IP address or the router IP address.  " +
                                    b"For Windows, open a command prompt on the host and type 'ipconfig /all'. For Mac host, select Apple -> About this Mac ->System Report -> Network.  " +
                                    b"For Linux, open a command prompt and type 'ip -4 addr' for IPv4 address and 'ip -0 addr' to find the Physical Address (i.e. MAC address).<br>" +
                                    b"Make note of the IPv4 Address and the Physical Address (i.e. MAC address).<br>")
                            qs["IPv4_external_address"] = ipaddr["ipv4"]["public"]
                            # no NAT?
                            if ipaddr["ipv4"]["local"] == ipaddr["ipv4"]["public"]:
                                msg += ("Direct connection to the Internet was detected.  No NAT router configuration needed.  However, two TCP ports are needed, 5061 and 38443.  "+
                                  "Please make sure any firewalls allow the needed ports through.  Select \"Next\" to continue.").encode('utf_8')
                                qs["nextpage"] = "wizmenu-" + str(5)
                            else:
                                msg += ("Login to your local router using your web browser.  " +
                                  "If you have trouble and you purchased your router, perform an internet search of the make and model of the router.  If your router was provided by your ISP, " +
                                  "try contacting them for support.  Select \"Next\" once you are logged in.").encode('utf_8')
                        # dhcp reserve/static
                        case 3:
                            ipaddr=get_ip()
                            if os.system("ping -c 1 host.docker.internal") == 0:
                                ipaddr["ipv4"]["mac"] = "(unknown)"
                                ipaddr["ipv4"]["local"] = "(unknown)"
                            msg += ("In your router, you should reserve this host's IPv4 address (noted earlier) so it will not change.  "+
                              "It is associated with the MAC address (also noted earlier) of this host.  In most routers, it is in the DHCP section and called \"static\" or \"reserved\".  "+
                              "If the IP address is not properly reserved, the router may assign this host a different IPv4 address after the next power cycle.  That would cause issues with IPv4.  "+
                              "This setting cannot be tested without powering off this host and the router for a significant amount of time.  "+
                              "If you have trouble and you purchased your router, perform an internet search of the make and model of the router.  If your router was provided by your ISP, "+
                              "try contacting them for support.  If this host is your router, this step can be skipped.  Select \"Next\" once done.").encode('utf_8')
                        # port forward
                        case 4:
                            ipaddr=get_ip()
                            if os.system("ping -c 1 host.docker.internal") == 0:
                                ipaddr["ipv4"]["local"] = "(unknown)"
                            msg += ("In your router, you need to configure IPv4 port forwarding.  The router needs to forward IPv4 TCP/IP packets from the Internet to this host's local IPv4 address "+
                              "noted earlier.  Two TCP ports are needed, 5061 and 38443.  The port forward settings are usually in the firewall or advanced section of the router settings.  "+
                              "If you have trouble and you purchased your router, perform an internet search of the make and model of the router.  If your router was provided by your ISP, "+
                              "try contacting them for support.  The configuration will be tested.  If this host is your router, then it will be firewall traffic rules, not port forward.  "+
                              "Select \"Next\" once done.").encode('utf_8')
                        # intro test
                        case 5:
                            class WizForm(MainForm):
                                http_proxy = StringField("HTTP proxy URL")
                                tor = BooleanField("Use Tor")
                            try:
                                rslt = requests.get("http://pubproxy.com/api/proxy?country=US,CA")
                                if rslt.status_code != 200 or not is_json(rslt.text):
                                    msg += ("Warning!  Failed to get initial http proxy from pubproxy.com.<br>").encode('utf_8')
                                    msg += ("The pubproxy.com site returned: "+rslt.text+".<br>").encode('utf_8')
                                else:
                                    rsltj = rslt.json()
                                    qs["http_proxy"] = rsltj["data"][0]["type"]+"://"+rsltj["data"][0]["ipPort"]
                            except Exception as e:
                                msg += ("Warning!  Failed to get initial http proxy from pubproxy.com.<br>").encode('utf_8')
                                msg += ("The pubproxy.com error: "+str(e)+".<br>").encode('utf_8')
                            msg += ("Network connectivity will now be tested for remote and local access to this container via the public IP addresses.  "+
                              "The remote test uses a free unauthenticated http proxy service.  Many are available, but they often come and go and may be unreliable.  "+
                              "You can find your own via <a href='https://www.google.com/search?q=free+unauthenticated+http+proxy+service' target='_blank'>Google</a>.  "+
                              "If the above entered http proxy fails, alternates from pubproxy.com will be tried.  "+
                              "If you select Tor, the Tor services will be used.  "+
                              "However, Tor needs more memory than the usual 100m granted to the container.  For Tor, 200m or more is recommended.  "
                              "Select \"Next\" to begin test.").encode('utf_8')
                        # test run...results
                        case 6:
                            _logger.debug(str(("fwtest:",qs)))
                            testscript = '/scripts/fwtestviaproxy.sh'
                            if qs.get("tor","n") == "y":
                                testscript = '/scripts/fwtestviator.sh'
                            http_proxy = qs.get("http_proxy","http://127.0.0.1:8080")
                            def process_status(process_name):
                                try:
                                    subprocess.check_output(["pgrep", process_name])
                                    return True
                                except subprocess.CalledProcessError:
                                    return False
                            tmp = '/tmp/fwout.log'
                            if (os.path.exists(tmp)):
                                _logger.debug(str(("fwtest: found",tmp)))
                                with open(tmp, "r") as f:
                                    lines = f.readlines()
                                if process_status('fwtestvia'):
                                    _logger.debug(str(("fwtest: running...",)))
                                    msg += "Testing still in progress...  Wait a few more seconds and click Next.<br>\n".encode('utf_8')
                                    qs["nextpage"] = "wizmenu-" + str(6)
                                else:
                                    os.remove(tmp)
                                    _logger.debug(str(("fwtest: NOT running...",)))
                                    if any('Congratulations!' in line for line in lines):
                                        msg += "Success!  Select \"Next\" to continue.<br>\n".encode('utf_8')
                                    else:
                                        msg += "Failed!  Select \"Next\" to try again.<br>\n".encode('utf_8')
                                        qs["nextpage"] = "wizmenu-" + str(4)
                                msg += b"Details are as follows:<br>"
                                msg += '<br>'.join(lines).encode('utf_8')
                            else:
                                _logger.debug(str(("fwtest: Temp file NOT found",tmp)))
                                ipaddr=get_ip()
                                msg += "testing...  Wait a few more seconds and click Next.<br>\n".encode('utf_8')
                                qs["nextpage"] = "wizmenu-" + str(6)
                                testurls = [ipaddr["ipv4"]["public"]+":5061",ipaddr["ipv4"]["public"]+":38443"]
                                process = subprocess.Popen("sudo HTTP_PROXY="+http_proxy+" "+testscript+" "+' '.join(testurls)+" >"+tmp, shell=True)
                                _logger.debug(str(("fwtest: started",process.args)))
                            _logger.debug(str(("fwtest: ...",form)))
                        # DDNS sign-up
                        case 7:
                            apikey = get_global("DNSTOKEN")
                            qs["api_key"] = apikey
                            class WizForm(MainForm):
                                api_key = StringField("API Key")
                            msg += ("You need to sign up with a free Dynamic Domain Name System (DDNS) service. "+
                              "The tested and supported free provider is Dynu at the "+("https://dynu.com" if lynx else "<a href='https://dynu.com' target='_blank'>https://dynu.com</a>")+" web site.  "+
                              "Visit their web site and under the DDNS menu, select sign up.  For option 1, type a preferred host name.  Examples would be \"gregsmmsgate\" or \"sallysopensips\".  "+
                              "Select a different top level domain if desired.  Click add.  You will be prompted to create an account.  Fill out the prompts as needed, making note of your username and password.  "+
                              "Click submit and perform the verifications as needed.  Once verified and logged into Dynu, click the gears in the upper-right of Dynu's web page.  "+
                              "Click \"API Credentials\".  In the list of existing API Credentials, to the right of  \"API Key\", click the view (binoculars) button.  "+
                              "The key will appear as a long string of random characters.  Highlight it and copy it to your clipboard.  Paste it into the prompt of the above dialog.  "+
                              ("To paste into this SSH session, you may need to right-click.  " if lynx else "")+
                              "Select \"Next\" once you have filled in the API Key.").encode('utf_8')
                        # pick DNS name
                        case 8:
                            dnsname = get_global("DNSNAME")
                            qs["ddns_name"] = dnsname
                            apikey = qs.get("api_key","")
                            if apikey == "":
                                apikey = get_global("DNSTOKEN")
                            if apikey != "":
                                rslt = requests.get("https://api.dynu.com/v2/dns",headers={"accept":"application/json","API-Key":apikey})
                                if rslt.status_code != 200 or not is_json(rslt.text):
                                    msg += ("Error!  Testing of the API key failed: "+str(rslt.status_code)+"<br>"+rslt.text+"<br>Select \"Next\" to try again.").encode('utf_8')
                                    qs["nextpage"] = "wizmenu-" + str(7)
                                else:
                                    rsltj = rslt.json()
                                    if len(rsltj["domains"]) > 0:
                                        set_global("DNSTOKEN",apikey)
                                        choices=[]
                                        for dom in rsltj["domains"]:
                                            choices+=[(dom["name"],dom["name"])]
                                        class WizForm(MainForm):
                                            ddns_name = SelectField("DDNS Name",choices=choices)
                                        msg += ("The API Key was entered correctly and access was successful!  "+
                                          "Please select the DDNS name you want to use from the above pull-down.  Select \"Next\" when ready.").encode('utf_8')
                                    else:
                                        msg += ("Error!  The API Key was entered correctelly and access was successful! However, no DDNS host names were found.  "+
                                          "Please add one or more from the Dynu web site by clicking the gears in the upper-right, "+
                                          "then \"DDNS Services\" and click \"+Add\".  Once done, select \"Next\" to try again.  ").encode('utf_8')
                                        qs["nextpage"] = "wizmenu-" + str(7)
                            else:
                                msg += ("Error!  Testing of the API key failed: No API key<br>Select \"Next\" to try again.").encode('utf_8')
                                qs["nextpage"] = "wizmenu-" + str(7)
                        # cert intro
                        case 9:
                            dnsname = qs.get("ddns_name","")
                            if dnsname == "":
                                dnsname = get_global("DNSNAME")
                            if dnsname != "":
                                set_global("DNSNAME",dnsname)
                                msg += ("The DDNS name " + dnsname + " was selected and will be used.<br>").encode('utf_8')
                                email = get_global("EMAIL")
                                qs["email"] = email
                                class WizForm(MainForm):
                                    email = StringField("eMail Address")
                                msg += ("A certificate is required.  It allows secure encrypted communications.  Let's Encrypt requests an email address for issuing a free certificate.  "+
                                  "Enter your email address in the above dialog.  Once entered, a certificate will be requested.  This container will automatically request renewal of the "+
                                  "certificate 30 days before the expiration.  Select \"Next\" when ready.").encode('utf_8')
                            else:
                                msg += ("A DDNS name was not selected.  Select \"Next\" to try again.<br>").encode('utf_8')
                                qs["nextpage"] = "wizmenu-" + str(8)
                        # cert generate
                        case 10:
                            email = qs.get("email","")
                            if email == "":
                                email = get_global("EMAIL")
                            if email != "":
                                msg += ("The eMail address " + email + " was entered and will be used.<br>").encode('utf_8')
                                set_global("EMAIL",email)
                                r = subprocess.run(["sudo","/scripts/certs.sh","-o"],capture_output=True)
                                _logger.debug(str(("/scripts/certs.sh:",r)))
                                if r.returncode == 0:
                                    msg += ("Certificate issued successfully. Select \"Next\" to continue. Details below: <br>").encode('utf_8')
                                    msg += r.stdout.replace(b'\n',b'<br>\n')
                                else:
                                    msg += ("Issue reported from certs.sh. Select \"Next\" to continue. Details below: <br>").encode('utf_8')
                                    msg += r.stdout.replace(b'\n',b'<br>\n')
                                    qs["nextpage"] = "wizmenu-" + str(9)
                            else:
                                msg += ("Error! The eMail address is missing or bad.<br>Select \"Next\" to try again.").encode('utf_8')
                                qs["nextpage"] = "wizmenu-" + str(9)
                        # config Voip.ms API
                        case 11:
                            dnsname = get_global("DNSNAME")
                            voipid = get_global("APIID")
                            voippw = get_global("APIPW")
                            qs["voipid"] = voipid
                            qs["voippw"] = voippw
                            class WizForm(MainForm):
                                voipid = StringField("Voip.ms User ID")
                                voippw = StringField("Voip.ms API Password",render_kw = {'type': 'password'})
                            msg += ("The Voip.ms API must be enabled.  Logon to the "+("https://voip.ms" if lynx else "<a href='https://voip.ms' target='_blank'>https://voip.ms</a>")+
                              " web site and select \"Main Menu->SOAP and REST/JSON API\".  If API is not already enabled, "+
                              "click \"Enable/Disable API\".  Enter, confirm and make note of an API password, click \"Save API Password\".  For \"Enable IP Address\", paste \""+dnsname+
                              "\" and click \"Save IP Addresses\".  Enter your Voip.ms ID and the API password above.  Select \"Next\" here when done and ready to provide credentials to MMSGate. ").encode('utf_8')
                        # Voip.ms test
                        case 12:
                            import urllib.parse
                            apiid = qs.get("voipid","")
                            if apiid == "":
                                apiid = get_global("APIID")
                            apipw = qs.get("voippw","")
                            if apipw == "":
                                apipw = get_global("APIPW")
                            try:
                                url="https://voip.ms/api/v1/rest.php?api_username={}&api_password={}&method=getSubAccounts"
                                r = requests.get(url.format(apiid,urllib.parse.quote(apipw)))
                                if is_json(r.text):
                                    rslt = r.json()
                                    if rslt["status"] == "success":
                                        if len(rslt["accounts"]) == 0:
                                            raise RuntimeError("No sub accounts found at https://voip.ms")
                                        else:
                                            msg += ("The Voip.ms ID and API password were entered successfully.<br>Select \"Next\" to continue.").encode('utf_8')
                                            set_global("APIID",apiid)
                                            set_global("APIPW",apipw)
                                    else:
                                        raise RuntimeError(rslt["status"])
                                else:
                                    raise RuntimeError("Data returned from Voip.ms API not in JSON format.")
                            except Exception as e:
                                msg += ("Error getting list of sub accounts: "+str(e)+".<br>Select \"Next\" to try again.").encode('utf_8')
                                qs["nextpage"] = "wizmenu-" + str(11)
                        # restart and done!
                        case 13:
                            msg += ("Congratulations!  MMSGate is now configured.  The next steps are to optionally add Linphone accounts for push notifications.  Push notification is optional.  "+
                              "Also set MMSGate preferences for the Voip.ms sub accounts.  Then finally configure clients.  Note: Configuring clients is done from the sub accounts menu.<br>"+
                              "OpenSIPS has been enabled and the container re-started. Select \"Next\" to return to the main menu.").encode('utf_8')
                            set_global("ENABLEOPENSIPS","Y")
                            subprocess.Popen(['sudo','/scripts/restart.sh'])
                            qs["nextpage"] = "mainmenu"
                        case _:
                            msg += ("Oops... ").encode('utf_8')
                    form = WizForm(data=qs)
                    form.button1.label.text = "Next"
                    form.button2.label.text = "Cancel"

                case "voipmsmenu":
                    # open the db
                    conn = get_dbconn()
                    if conn is None:
                        raise Exception("MMSGate db connection failed.")
                    # create table/indexes if needed
                    init_linphonedb(conn)
                    try:
                        # first run (or refreshed)
                        if qs["thispage"] == "mainmenu":
                            msg += init_subacctdb(conn)
                        # applied some settings for a row?
                        if qs["thispage"] == "subacctmenu":
                            # update the SMS/MMS accepting and the linphone push notif acct
                            username,sms_mms,pushnotification = qs["tmpdata"]
                            sms_mms = 0 if sms_mms == "Ignore" else 1
                            if pushnotification == "N/A":
                                pushnotification = None
                            try:
                            # update db
                                conn.execute("UPDATE subacct SET smsmms = ?, linphone = ? WHERE account = ?;",
                                  (sms_mms,pushnotification,username))
                                conn.commit()
                                msg += ("SMS/MMS and Push Notification Linphone account preferences updated for sub account " + username + ".<br>").encode('utf_8')
                            except Exception as e:
                                msg = ('Sub account preferences update error: '+str(e)).encode('utf_8')
                                _logger.error(str(("Sub account preferences update error:",e)))
                        # get fresh did to accts
                        get_did_accts(conn,ask_q)
                        qs["thispage"] = "voipmsmenu"
                        # list to display in form
                        accts = conn.execute("SELECT account,callerid,CASE smsmms WHEN 0 THEN 'Ignore' ELSE 'Accept' END, IFNULL(linphone,'N/A'), "+
                            "CASE ext WHEN '' THEN 'null' ELSE ext END, CASE tls WHEN 0 THEN 'N/A' ELSE 'TLS' END, internal_cnam FROM subacct ORDER BY callerid,account;").fetchall()
                        _logger.debug(str(("Voip.ms select:",accts)))
                        # unused linphone accts for the PN select field
                        choices = conn.execute("SELECT username,username FROM linphone WHERE activated = 1 ORDER BY username").fetchall()
                        _logger.debug(str(("Voip.ms choices select:",choices)))
                        conn.close()
                        # for form display
                        qs["nextpage"] = "voipmsmenu"
                        pagetitle = b' - Manage Voip.ms Sub Accounts'
                        _logger.debug(str(("Voip.ms qs:",qs)))

                        # these 2 classes are for voip.ms subacct config
                        class SubVoipmsForm(Form):
                            subaccount = StringField("Sub Account")
                            username = HiddenField()
                            callerid = StringField("DID/CallerID")
                            sms_mms = SelectField("SMS/MMS",choices=[('Ignore','Ignore'),('Accept','Accept')])
                            pushnotification = SelectField("Push Notif")
                            apply = SubmitField()
                            extension = StringField()
                            internal_cnam = StringField()
                            encryption = StringField()
                            client_config = SubmitField()
                            note = StringField()
                        class VoipmsForm(MainForm):
                            voipms = FieldList(FormField(SubVoipmsForm))

                        form = VoipmsForm(data=qs)
                        form.button1.label.text = "Refresh"
                        form.button2.label.text = "Cancel"

                        # populate rows
                        tableheader = True
                        for subaccount, callerid, sms_mms, pushnotification, extension, encryption, internal_cnam in accts:
                            note = ""
                            if encryption == "N/A":
                                note+="Please enable 'Encrypted SIP Traffic' for sub account.  "
                            if extension == "null":
                                note+="Please enter an 'Internal Extension Number' for sub account.  "
                            if not callerid:
                                note+="Please select a DID for 'CallerID Number' for sub account.  "
                            f = form.voipms.append_entry({"subaccount":subaccount, "username":subaccount, "callerid":callerid, "sms_mms":sms_mms,
                              "pushnotification":pushnotification, "extension":extension, "encryption":encryption, "internal_cnam":internal_cnam, "note":note})
                            f["subaccount"].flags.justtxt = True
                            f["callerid"].flags.justtxt = True
                            f["pushnotification"].choices = [("N/A","N/A")] + choices
                            f["extension"].flags.justtxt = True
                            f["encryption"].flags.justtxt = True
                            f["internal_cnam"].flags.justtxt = True
                            f["note"].flags.justtxt = True
                            if extension == "null" or encryption == "N/A" or not callerid:
                                f["client_config"].render_kw = {'disabled': 'disabled'}
                    except Exception as e:
                        msg = ('Enter verification code error: '+str(e)).encode('utf_8')
                        _logger.error(str(("Enter verification code error:",e)))

                case "deletemenu":
                    # after response, come back to original page
                    qs["nextpage"] = qs["thispage"]
                    qs["thispage"] = "deletemenu"
                    form = MainForm(data=qs)
                    form.button1.label.text = "Yes"
                    form.button2.label.text = "No"

                case "linmenu":
                    # open the db
                    conn = get_dbconn()
                    if conn is None:
                        raise Exception("MMSGate db connection failed.")
                    # create table/indexes if needed
                    init_linphonedb(conn)
                    # email verify code?
                    if qs["thispage"] == "verifymenu":
                        username,verify = qs["tmpdata"]
                        try:
                            password, = conn.execute("SELECT password FROM linphone WHERE username = ?;",(username,)).fetchone()
                            rslt = requests.post("https://subscribe.linphone.org/api/accounts/me/email",headers={"from":"sip:"+username+"@sip.linphone.org",
                              "Content-Type":"application/json","accept":"application/json"},auth=requests.auth.HTTPDigestAuth(username, password),json={"code":verify})
                            # did it work?
                            if is_json(rslt.text) and "id" in rslt.json():
                                rsltj = rslt.json()
                                # update db
                                conn.execute("UPDATE linphone SET activated = ?, provisioning_token = ? WHERE username = ?;",
                                  (rsltj["activated"],rsltj["provisioning_token"],username))
                                conn.commit()
                                msg = ("Confirmed "+username+" email successfully").encode('utf_8')
                            else:
                                # update db
                                conn.execute("UPDATE linphone SET email = NULL, account_creation_token = 'dummy' WHERE username = ?;",
                                  (username,))
                                conn.commit()
                                msg = ("Confirmation of "+username+" email FAILED!  Email removed.").encode('utf_8')
                        except Exception as e:
                            msg = ('Enter verification code error: '+str(e)).encode('utf_8')
                            _logger.error(str(("Enter verification code error:",e)))
                    # set email address?
                    if qs["thispage"] == "emailmenu":
                        username,email = qs["tmpdata"]
                        try:
                            password, = conn.execute("SELECT password FROM linphone WHERE username = ?;",(username,)).fetchone()
                            rslt = requests.post("https://subscribe.linphone.org/api/accounts/me/email/request",headers={"from":"sip:"+username+"@sip.linphone.org",
                              "Content-Type":"application/json","accept":"application/json"},auth=requests.auth.HTTPDigestAuth(username, password),json={"email":email})
                            # did it work?
                            if rslt.status_code == 200:
                                conn.execute("UPDATE linphone SET account_creation_token = NULL, email = ? WHERE username = ?;",(email,username))
                                conn.commit()
                                msg = ("Set email for " + username + " to address " + email + " was successful.").encode('utf_8')
                            else:
                                _logger.error(str(("Set email fail:",rslt.status_code,rslt.text)))
                                msg = ("Set email for " + username + " to address " + email + " has FAILED!").encode('utf_8')
                        except Exception as e:
                            msg = ('Set email error: '+str(e)).encode('utf_8')
                            _logger.error(str(("Set email error:",e)))
                    # was there a password update?
                    if qs["thispage"] == "updatemenu":
                        try:
                            username,password = qs["tmpdata"]
                            # try the password to get details
                            rslt = requests.get("https://subscribe.linphone.org/api/accounts/me",headers={"from":"sip:"+username+"@sip.linphone.org",
                              "Content-Type":"application/json","accept":"application/json"},auth=requests.auth.HTTPDigestAuth(username, password))
                            # did pw work?
                            if is_json(rslt.text) and "id" in rslt.json():
                                rsltj = rslt.json()
                                _logger.debug(str(("Admin method: Trying password:",rsltj)))
                                conn.execute("UPDATE linphone SET password = ?, domain = ?, email = ?, activated = ?, provisioning_token = ?, account_creation_token = ? WHERE username = ?;",
                                  (password,rsltj["domain"],rsltj["email"],rsltj["activated"],rsltj["provisioning_token"],'dummy' if not rsltj["email"] else None,username))
                                conn.commit()
                                msg = ("Password entry of account "+username+" was successful.").encode('utf_8')
                            else:
                                msg = ("Incorrect password for account "+username+". Please try again.").encode('utf_8')
                        except Exception as e:
                            msg = ('Update Linphone account password error: '+str(e)).encode('utf_8')
                            _logger.error(str(("Admin linacct password update error:",e)))
                    # captcha solved?
                    if qs["thispage"] == "solvedmenu":
                        try:
                            username, password, account_creation_request_token = conn.execute("SELECT username,password,account_creation_request_token FROM linphone WHERE username = ?;",(qs["tmpdata"],)).fetchone()
                            rslt = requests.post("https://subscribe.linphone.org/api/account_creation_tokens/using-account-creation-request-token",
                              headers={"Content-Type":"application/json","accept":"application/json"},json={"account_creation_request_token":account_creation_request_token})
                            if is_json(rslt.text) and "token" in rslt.json():
                                rsltj = rslt.json()
                                account_creation_token = rsltj["token"]
                                rslt = requests.post("https://subscribe.linphone.org/api/accounts/with-account-creation-token",
                                  headers={"Content-Type":"application/json","accept":"application/json"},json={"username":username,"password":password,"algorithm":"MD5","account_creation_token":account_creation_token})
                                if is_json(rslt.text) and "id" in rslt.json():
                                    rsltj = rslt.json()
                                    conn.execute("UPDATE linphone SET account_creation_request_token = NULL, captcha_url = NULL, domain = ?, account_creation_token = ? WHERE username = ?;",
                                      (rsltj["domain"],account_creation_token,username))
                                    conn.commit()
                                else:
                                    msg = ('Create account failed: '+rslt.text).encode('utf_8')
                            else:
                                msg = ('Solve captcha failed: Did you actually solve the CAPTCHA?').encode('utf_8')
                        except Exception as e:
                            msg = ('Solve captcha error: '+str(e)).encode('utf_8')
                            _logger.error(str(("Solve captcha error:",e)))
                    # was there an add?
                    if qs["thispage"] == "addmenu":
                        try:
                            # insert into db
                            username,password,token,validation_url = qs["tmpdata"]
                            conn.execute("INSERT INTO linphone(username,password,account_creation_request_token,captcha_url) VALUES(?,?,?,?);",(username,password,token,validation_url))
                            conn.commit()
                            if password:
                                if lynx:
                                    msg = ("Requested "+username+"@sip.linphone.org added successfully as new account.  Solve the CAPTCHA please: "+validation_url).encode('utf_8')
                                else:
                                    msg = ("Requested "+username+"@sip.linphone.org added successfully as new account.  Solve the CAPTCHA please: <a href='" + validation_url + "' target='_blank'>Solve CAPTCHA</a>").encode('utf_8')
                            else:
                                msg = b'Added ' + username.encode('utf_8') + b'@sip.linphone.org successfully as existing account.  Please enter current password.'
                        except Exception as e:
                            msg = ('Add Linphone account failed: '+str(e)).encode('utf_8')
                            _logger.error(str(("Admin linacct add error:",e)))
                    # was there a delete?
                    if qs["thispage"] == "deletemenu" and "Yes" in qs.values():
                        try:
                            conn.execute("DELETE FROM linphone WHERE username=?", (qs["passdata"],))
                            conn.commit()
                            msg = ('Deleted Linphone account '+qs["passdata"]).encode('utf_8')
                        except Exception as e:
                            msg = ('Delete Linphone account failed: '+str(e)).encode('utf_8')
                            _logger.error(str(("Admin linacct delete error:",e)))
                    # form will be filled w/ linphone accts
                    linusers = conn.execute("SELECT username, CASE WHEN activated=1 THEN 'Activated!' WHEN password IS NULL THEN 'Enter password' WHEN account_creation_request_token IS NOT NULL THEN captcha_url "+
                      "WHEN account_creation_token IS NOT NULL THEN 'Add email' WHEN email IS NOT NULL THEN 'Verify email' ELSE '' END FROM linphone ORDER BY username;").fetchall()
                    conn.close()
                    _logger.debug(str(("Admin method: query linphone:",linusers)))
                    # generate linphone page
                    qs["nextpage"] = "linmenu"
                    qs["thispage"] = "linmenu"
                    pagetitle = b' - Manage Linphone Accounts'
                    _logger.debug(str(("Admin qs:",qs)))

                    # these 2 classes are for editing Linphone accounts
                    class SubLinForm(Form):
                        linacct = StringField()
                        username = HiddenField()
                        token = HiddenField()
                        status = StringField()
                        entry = StringField()
                        action = SubmitField()
                        delete = SubmitField()
                    class LinForm(MainForm):
                        lin = FieldList(FormField(SubLinForm))

                    form = LinForm(data=qs)
                    form.button1.label.text = "Refresh"
                    form.button2.label.text = "Cancel"

                    # fill the form with db select results
                    tableheader = True
                    for linid,status in linusers:
                        f = form.lin.append_entry({"linacct":linid,"status":status ,"entry":"","username":linid})
                        f["linacct"].flags.justtxt = True
                        f["status"].flags.justtxt = True
                        if status == "Activated!":
                            f["entry"].flags.justtxt = True
                            f["action"].render_kw = {'disabled': 'disabled'}
                        if status.startswith("http"):
                            if not lynx:
                                f["status"].data = "<a href='" + status + "' target='_blank'>Solve CAPTCHA</a>"
                            f["entry"].flags.justtxt = True
                            f["action"].label.text = "Solved"
                        if status == "Add email":
                            f["action"].label.text = "Set email"
                        if status == "Enter password":
                            f["action"].label.text = "Set Password"
                            f["entry"].render_kw = {'type': 'password'}
                        if status == "Verify email":
                            f["action"].label.text = "Enter code"
                    # for adding an acct
                    f = form.lin.append_entry({"linacct":"","status":"","entry":""})
                    f["delete"].render_kw = {'disabled': 'disabled'}
                    f["entry"].flags.justtxt = True
                    f["status"].flags.justtxt = True
                    f["action"].label.text = "Add"

                case _:
                    _logger.error(str(("Admin error: default for nextpage")))

        except Exception as e:
            PrintException(e)
            _logger.error(str(("Admin wizard next form:",e)))

        # generate the html page to display
        if form:
            _logger.debug("Admin form: " + str(type(form)))
            r = b'''<!DOCTYPE html><html><head><title>MMSGate Admin</title></head><body><h2>MMSGate Admin ''' + pagetitle + b'''</h2>\n'''
            r += form.get_html(cfg.get("web","pathadmin"),tableheader)
            r += msg
            r += b'''</body></html>'''
        else:
            r = b'''<!DOCTYPE html><html><body><h2>Error! No form</h2></body></html>'''

        # return the html
        return r

# get db
def get_dbconn():
    import sqlite3
    # open the db
    dbfile = os.path.expanduser(cfg.get("mmsgate","dbfile"))
    try:
        conn = sqlite3.connect(dbfile)
    except sqlite3.Error as e:
        _logger.error(str("Error get_dbconn: "+str(e)))
    return conn

def init_linphonedb(conn):
    # create table/indexes if needed
    conn.execute("CREATE TABLE IF NOT EXISTS linphone (username TEXT UNIQUE, password TEXT, account_creation_request_token TEXT, account_creation_token TEXT, "+
      "provisioning_token TEXT, captcha_url TEXT, activated INT DEFAULT 0, email TEXT, domain TEXT DEFAULT 'sip.linphone.org');")
    conn.execute("CREATE INDEX IF NOT EXISTS lp_user ON linphone (username);")

# return the global setting
def get_global(globalsetting):
    import subprocess
    sp = subprocess.run(["bash","-c",". /etc/opensips/globalcfg.sh; echo $"+globalsetting+";"],capture_output=True)
    return sp.stdout.decode("utf-8").strip()

# set the global setting
def set_global(globalsetting,globalvalue):
    os.system("sudo /etc/opensips/globalcfg.sh gup "+globalsetting+" "+globalvalue)

# build did to accts dict for sending inbound messages to did out to the clients
def get_did_accts(conn,ask_q):
    accts = conn.execute("SELECT account,callerid FROM subacct WHERE smsmms = 1 AND ext != '' AND tls != 0 ORDER BY callerid,account;").fetchall()
    _logger.debug(str(("DID accts select:",accts)))
    did_accts_tmp = {}
    for acct,callerid in accts:
        did_accts_tmp[callerid] = did_accts_tmp.get(callerid,[]) + [acct]
    _logger.debug(str(("DID accts final:",did_accts_tmp)))
    ask_q.put(("PutAccts",did_accts_tmp))

# init subacct table w/ accts from voip.ms
def init_subacctdb(conn):
    import urllib.parse

    import requests

    # build tables if needed
    conn.execute("CREATE TABLE IF NOT EXISTS subacct (account TEXT UNIQUE, password TEXT, callerid TEXT, ext TEXT, smsmms INT DEFAULT 1, " +
      "linphone TEXT, uuid TEXT, tls TEXT, max_expiry TEXT, internal_cnam TEXT, description TEXT);")
    conn.execute("CREATE INDEX IF NOT EXISTS sa_acct ON subacct (account);")
    conn.execute("CREATE INDEX IF NOT EXISTS sa_linacct ON subacct (linphone,account);")

    # get params
    apiid = get_global("APIID")
    apipw = get_global("APIPW")
    dnsname = get_global("DNSNAME")
    if apiid == "" or apipw == "" or dnsname == "":
        return b'Warn: Global names APIID, APIPW or DNSNAME are empty.'

    # webhook path needed for did setting at https://voip.ms
    webpostpath = cfg.get("web","protocol")+"://"+dnsname+":"+str(cfg.get("web","webport"))+cfg.get("web","pathpost")
    _logger.debug(str(("init_subacctdb() webpostpath: ",webpostpath)))

    # msg to return
    msg = ""

    dids = {}
    # get a list of DIDs
    try:
        url="https://voip.ms/api/v1/rest.php?api_username={}&api_password={}&method=getDIDsInfo"
        r = requests.get(url.format(apiid,urllib.parse.quote(apipw)))
        if is_json(r.text):
            rsltd = r.json()
            if rsltd["status"] == "success":
                for did in rsltd["dids"]:
                    dids[did["did"]] = did["description"]
                    if did["webhook"] != webpostpath or did["webhook_enabled"] != "1":
                        msg += "Please enable 'SMS/MMS Webhook URL' for DID " + did["did"] + " and set it to " + webpostpath + " .<br>\n"
                    if did["sms_enabled"] != "1":
                        msg += "Please enable 'Message Service (SMS/MMS)' for DID " + did["did"] + " . <br>\n"
            else:
                raise RuntimeError(str(("init_subacctdb() request getDIDsInfo returned: ",r,r.text)))
        else:
            raise RuntimeError(str(("init_subacctdb() request getDIDsInfo returned: ",r,r.text)))
    except Exception as e:
        _logger.error(str(e))
        return b'Error getting DID info.<br>'

    # temp table for initial sub acct load
    conn.execute("CREATE TEMPORARY TABLE tsubacct (account TEXT UNIQUE, password TEXT, callerid TEXT, ext TEXT, tls TEXT, max_expiry TEXT, internal_cnam TEXT, description TEXT);")

    # get list of sub accts
    try:
        url="https://voip.ms/api/v1/rest.php?api_username={}&api_password={}&method=getSubAccounts"
        r = requests.get(url.format(apiid,urllib.parse.quote(apipw)))
        if is_json(r.text):
            rslt = r.json()
            if rslt["status"] == "success":
                if len(rslt["accounts"]) == 0:
                    raise RuntimeError("No sub accounts found at https://voip.ms")
            else:
                raise RuntimeError(rslt["status"])
        else:
            raise RuntimeError(str(("Data returned from Voip.ms API not in JSON format.",r,r.text)))
    except Exception as e:
        _logger.error(str(e))
        return b'Error getting list of sub accounts.<br>'

    # fill temp table w/ current sub accounts
    for acct in rslt["accounts"]:
        if acct["internal_extension"] != "":
            ext = "10"+acct["internal_extension"]
        else:
            ext = ""
        if acct["callerid_number"] not in dids:
            cid = None
        else:
            cid = acct["callerid_number"]
        # internal_cnam description
        conn.execute("INSERT INTO tsubacct (account,password,callerid,ext,tls,max_expiry,internal_cnam,description) VALUES(?,?,?,?,?,?,?,?)",
          (acct["account"],acct["password"],cid,ext,acct["sip_traffic"],acct["max_expiry"],acct["internal_cnam"],acct["description"]))
    conn.commit()

    # append any new sub accts
    conn.execute("INSERT OR IGNORE INTO subacct (account,password,callerid,ext,tls,max_expiry) SELECT account,password,callerid,ext,tls,max_expiry FROM tsubacct;")
    # delete any sub accts that don't exist any more at voip.ms
    conn.execute("DELETE FROM subacct WHERE account NOT IN (SELECT account FROM tsubacct);")
    # update the values that may have change at voip.ms
    conn.execute("UPDATE subacct SET password = t.password, callerid = t.callerid, ext = t.ext, tls = t.tls, max_expiry = t.max_expiry, " +
      "internal_cnam = t.internal_cnam, description = t.description FROM (SELECT * FROM tsubacct) AS t WHERE subacct.account = t.account;")
    conn.commit()

    return msg.encode('utf_8')

# return true if valid json string
def is_json(myjson):
    import json
    try:
        json.loads(myjson)
    except ValueError:
        return False
    return True

# this function will query the IP addresses via "ip addr" amd and return json data.
def get_ip():
    import json
    import subprocess
    r = subprocess.run(["/scripts/getaddr.sh","-j"],capture_output=True)
    _logger.debug(str(("Admin method get_ip:",r)))
    return json.loads(r.stdout)

# used for generic catch all exceptions
def PrintException(e):
    import linecache
    import traceback
    from io import StringIO
    exc_type, exc_obj, tb = sys.exc_info()
    with StringIO("") as s:
        traceback.print_exception(e,file=s)
        s.seek(0)
        traces = s.read()
    f = tb.tb_frame
    lineno = tb.tb_lineno
    filename = f.f_code.co_filename
    linecache.checkcache(filename)
    line = linecache.getline(filename, lineno, f.f_globals)
    _logger.critical("EXCEPTION IN ({}, LINE {} \"{}\"): \n{}".format(filename, lineno, line.strip(), traces))

# this class has the db thread and gets the AOR from OpenSIPS
class db_class():

    # look up the linphone user's address in the aor and return it's contact uri and other into
    def get_aor(self,addr):
        import json
        import subprocess
        from urllib.parse import urlparse
        try:
            sp = subprocess.run(["opensips-cli","-x","mi","ul_show_contact","location",addr],timeout=10,capture_output=True)
            if sp.returncode == 0:
                r = json.loads(sp.stdout)
                attr = json.loads(r['Contacts'][0]['Attr'])
                id,dom = urlparse(attr["tu"]).path.split("@")
                return id,dom
            else:
                _logger.warning("Get contact failed, run returned: "+str(sp))
        except Exception as e:
            PrintException(e)
        return None,None

    # get the object for this class ready
    def __init__(self):
        import queue
        import threading
        self.t = threading.Thread(name="DB-THREAD" , target=self.queue_db, daemon=True)
        self.db_q = queue.Queue()

    # start the thread
    def start(self):
        self.t.start()

    # thread loops forever for db activity
    def queue_db(self):
        import json
        import os
        import queue
        import sqlite3
        import subprocess
        from datetime import UTC, datetime, timedelta
        # try to open the db
        try:
            dbfile = os.path.expanduser(cfg.get("mmsgate","dbfile"))
            _logger.debug("DB setting "+cfg.get("mmsgate","dbfile")+" became "+dbfile)
            conn = sqlite3.connect(dbfile)
        except Exception as e:
            _logger.error("Failed to open DB file: "+cfg.get("mmsgate","dbfile")+":"+str(e))
            exit()
        def unixtime(s):
            import time
            return int(time.time())+s
        conn.create_function("unixtime", 1, unixtime)
        # table of messages.
        conn.execute("CREATE TABLE IF NOT EXISTS send_msgs (rcvd_ts INT DEFAULT (unixtime(0)), fromid TEXT, toid TEXT, fromdom TEXT, todom TEXT, msgtype TEXT, "+ \
          "did TEXT, direction TEXT, message TEXT, msgstatus TEXT DEFAULT 'QUEUED', sent_ts INT, init_ts INT DEFAULT (unixtime(0)), trycnt INT DEFAULT 0, msgid TEXT);")
        # indexes for selects and updates.  partial indexes are restricted to queued/active messages.
        conn.execute("CREATE INDEX IF NOT EXISTS sm_to ON send_msgs (toid,todom,rcvd_ts) WHERE msgstatus NOT IN ('200','202');")
        conn.execute("CREATE INDEX IF NOT EXISTS sm_stats1 ON send_msgs (direction,sent_ts,msgstatus);")
        conn.execute("CREATE INDEX IF NOT EXISTS sm_stats2 ON send_msgs (direction,init_ts);")
        conn.commit()
        # get one queued message (oldest) per destination.
        sql_select_pending = "SELECT rcvd_ts,sent_ts,fromid,fromdom,toid,todom,message,direction,msgstatus,did,min(rowid) as rowid,msgtype,trycnt "+ \
          "FROM send_msgs WHERE msgstatus NOT IN ('200','202') GROUP BY toid;"
        # updates for message status
        sql_update_status_via_rowid = "UPDATE send_msgs SET sent_ts = unixtime(0),msgstatus = ?, trycnt = trycnt + 1 WHERE rowid = ?;"
        sql_update_status_dom_via_rowid = "UPDATE send_msgs SET sent_ts = unixtime(0),msgstatus = ?, todom = ?, fromdom = ?, trycnt = trycnt + 1 WHERE rowid = ?;"
        # note: msgstatus is in where clause twice to get sqlite to use sm_hs index.
        sql_insert_new = "INSERT INTO send_msgs(fromid,fromdom,toid,todom,message,direction,did,msgtype,msgid) VALUES(?,?,?,?,?,?,?,?,?);"
        # amount of time before trying to send again
        td_timeout = timedelta(minutes=1)

        try:
            # loop forever
            while True:
                # get oldest queued messages for each destination (to)
                for rcvd_ts,sent_ts,fromid,fromdom,toid,todom,message,direction,msgstatus,did,rowid,msgtype,trycnt in conn.execute(sql_select_pending).fetchall():
                    _logger.debug(str(("SELECT record: ",rcvd_ts,sent_ts,fromid,fromdom,toid,todom,message,direction,msgstatus,did,rowid,msgtype)))
                    # queued means it is ready to try
                    if msgstatus == "QUEUED":
                        # going to linphone user?
                        if direction == "IN":
                                # try to look them up in AOR

                            newtoid,newtodom = self.get_aor(toid)
                            newfromdom = fromdom or newtodom
                            _logger.debug("get_aor returned: "+str((newtoid,newtodom)))
                            # got the AOR info
                            if newtoid:
                                    # send it!
                                if msgtype == "SMS":
                                    sp = subprocess.run(["opensips-cli","-x","mi","t_uac_dlg","method=MESSAGE","ruri=sips:{}@{};transport=tls".format(newtoid,newtodom),"next_hop=sips:{}".format("localhost"),
                                      "headers=To: sips:{}@{}\\r\\nFrom: sips:{}@{}\\r\\nContent-Type: text/plain\\r\\n".format(newtoid,newtodom,fromid,newfromdom),"body={}".format(message)],timeout=30,capture_output=True)
                                else:
                                    sp = subprocess.run(["opensips-cli","-x","mi","t_uac_dlg","method=MESSAGE","ruri=sips:{}@{};transport=tls".format(newtoid,newtodom),"next_hop=sips:{}".format("localhost"),
                                      "headers=To: sips:{}@{}\\r\\nFrom: sips:{}@{}\\r\\nContent-Type: application/vnd.gsma.rcs-ft-http+xml\\r\\n".format(newtoid,newtodom,fromid,newfromdom),"body={}".format(message)],
                                      timeout=30,capture_output=True)
                                _logger.debug("send message returned: "+str(sp))
                                # work?
                                if sp.returncode == 0:
                                    r = json.loads(sp.stdout)
                                    if r["Status"] == "200 Ok" or "202 Accepted":
                                        # worked!  put the 200 or 202 in the db
                                        self.update_row_db(conn,sql_update_status_dom_via_rowid,(r["Status"].split()[0],newtodom,newfromdom,rowid))
                                    else:
                                        # failed...
                                        self.update_row_db(conn,sql_update_status_via_rowid,(r["Status"],rowid))
                                else:
                                    self.update_row_db(conn,sql_update_status_via_rowid,("ERR",rowid))

                            # no AOR...
                            else:
                                _logger.debug("contact for "+toid+" not found ")
                                self.update_row_db(conn,sql_update_status_via_rowid,("Missing AOR",rowid))

                    # is it a message we tried before?  if timeout, then queue it back up.
                    if msgstatus != "QUEUED":
                        if msgstatus == "API ERR":
                            # time between retry will grow exponentially for issues w/ voip.ms
                            if (datetime.now(UTC) - datetime.fromtimestamp(sent_ts,UTC)) > (td_timeout * trycnt * trycnt):
                                self.update_row_db(conn,sql_update_status_via_rowid,("QUEUED",rowid))
                        else:
                            # try again soon
                            if (datetime.now(UTC) - datetime.fromtimestamp(sent_ts,UTC)) > td_timeout:
                                self.update_row_db(conn,sql_update_status_via_rowid,("QUEUED",rowid))

                # check the inter-process queue
                try:
                    item = self.db_q.get(timeout=10)
                    _logger.debug("From queue "+str(item))
                    # got a new message.  place it in the db as a queued message to send.
                    if item[0] == "MsgNew":
                        mtype,fromid,fromdom,toid,todom,message,direction,did,msgtype,msgid = item
                        cnt = conn.execute(sql_insert_new,(fromid,fromdom,toid,todom,message,direction,did,msgtype,msgid))
                        _logger.debug("Rows inserted: "+str(cnt))
                        conn.commit()
                    # shutdown?
                    if item[0] == "Done":
                        break
                except queue.Empty:
                    pass
        except Exception as e:
            PrintException(e)
        conn.close()
        _logger.warning("Exiting DB thread.")

    # run sql update
    def update_row_db(self,conn,sql,params):
        try:
            _logger.debug("Updating via: "+str(params))
            cnt = conn.execute(sql,params).rowcount
            conn.commit()
            if cnt == 1:
                _logger.debug("Rows updated: "+str(cnt)+" for update via "+str(params))
            else:
                _logger.warning("Rows updated: "+str(cnt)+" for update via "+str(params))
        except Exception as e:
            PrintException(e)

# this is the config class.  It had all the settings from config file and CLI switches
class config_class:
    import logging

    # for logging...
    loglevels = {"DEBUG":logging.DEBUG,"INFO":logging.INFO,"WARNING":logging.WARNING,"ERROR":logging.ERROR,"CRITICAL":logging.CRITICAL}

    # defaults to pick up if not specified
    defaults = {
      "web": {
        "webbind": "0.0.0.0",
        "webport": "38443",
        "protocol": "https",
        "bindoverride": "127.0.0.1:38080",
        "pathget": "/mmsmedia",
        "pathfile": "/file",
        "pathadmin": "/admin",
        "localmedia": "/data/mmsmedia",
        "pathpost": "/mmsgate"},
      "mmsgate": {
        "dbfile": "/data/mmsgate/mmsgate.sqlite",
        "logger": "WARNING"}}
    # descriptions for all the section/option settings in the config file
    descriptions = {
      "mmsgate": {"_section": "This section contains options related to the MSSGate application",
        "logger": "This is the logging level for the MMSGate.  Options are: DEBUG,INFO,WARNING,ERROR and CRITICAL",
        "loggerfile": "This is the log file for MMSGate, full path or use ~ for home. ",
        "dbfile": "This is the SQLite db file.  A ~ is allowed and will be expanded to home of current user."},
      "api": {"_section": "This section has options for the API method.",
        "apiid": "Required.  This is the logon id for the API.  It is the same as the VoIP.ms web site logon ID. ",
        "apipw": "Required.  This is the logon password for the API.  It is created at the VoIP.ms SOAP and REST/JSON API web page, https://voip.ms/m/api.php."},
      "web": {"_section": "This section has options for the WSGI web hook and MMS media interface",
        "protocol": "This is the web protocol, either http or https.",
        "cert": "This is the path to the certificate chain file.  It is needed for https protocol.",
        "key": "This is the path to the private key file.  It is needed for https protocol.",
        "bindoverride": "Setting this to a gunicorn bind setting tells mmsgate that TLS and web traffic are handled by a proxy server (nginx) and mmsgate will just do local http.  "+
          "Example is \"bindoverride=0.0.0.0:8080\".",
        "pathpost": "This is the path the web hook url uses.",
        "pathget": "This is the path the MMS media url uses.",
        "pathfile": "This is the path the MMS file server upload uses.",
        "pathadmin": "This is the path for the admin web interface.",
        "localmedia": "This is the local path for MMS media file storage.  A ~ can be used for home. The path must exist and account have r/w permissions.",
        "webbind": "This is the IP address to bind for the WSGI webhook service",
        "webport": "This is the port number for the WSGI webhook service",
        "webdns": "Required.  This is the DNS name of the webhook web server and MMS URL web server, i.e., this server."
      }}

    # these options all return int
    type_int = ["sipport","siploglevel","sipconsoleloglevel","webport"]

    # load the config file
    def load(self,filename):
        import configparser

        # set default values
        dfs = {}
        for s in self.defaults:
            dfs.update(self.defaults[s])

        self.configobj = configparser.ConfigParser()
        if os.path.exists(filename):
            self.configobj.read(filename)
        else:
            raise ValueError("Config file not found: "+filename)

        # check the config loaded against the descriptions to confirm valid
        for s in self.configobj.keys():
            if s != "DEFAULT":
                if s not in self.descriptions:
                    raise ValueError("Invalid section \"[" + s + "]\" found in config file " + filename + ". Please correct.")
                else:
                    for o in self.configobj[s].keys():
                        if o not in self.descriptions[s]:
                            raise ValueError("Invalid option \"" + o + "\" in section \"[" + s + "]\" found in config file " + filename + ". Please correct.")

        # now set defaults
        self.configobj["DEFAULT"] = dfs

    # does the section/option exist?  including default values.
    def exists(self,sect,opt):
        return self.configobj.has_option(sect,opt)

    # get the section/option value, including default values.
    def get(self,sect,opt):
        # make sure the section/option has been defined w/ description.
        if sect in self.descriptions:
            if "_section" in self.descriptions[sect]:
                if opt in self.descriptions[sect]:
                    # id it an int type?
                    if opt in self.type_int:
                        # return the int of the option
                        return self.configobj.getint(sect,opt,fallback=None)
                    else:
                        # return the string
                        return self.configobj.get(sect,opt,fallback=None)
                else:
                    # oops, forgot to add descriptions...
                    raise ValueError("Internal error.  Missing option \"" + opt + "\" in section \"[" + sect + "]\" from descriptions definition.")
            else:
                raise ValueError("Internal error.  Missing description for section \"[" + sect + "]\" from descriptions definition.")
        else:
            raise ValueError("Internal error.  Missing section \"[" + sect + "]\" from descriptions definition.")

    # print out the default config file.  all the sections will appear but options will be commented out.  Also, descriptions will appear as comments.
    def print_default_config(self):
        import configparser
        from io import StringIO
        self.configobj = configparser.ConfigParser()
        # populate the config obj w/ sections from descriptions
        for s in self.descriptions.keys():
            self.configobj.add_section(s)
            # add in each option key
            for o in self.descriptions[s].keys():
                # except the sections description
                if o != "_section":
                    # maybe with the default if available
                    try:
                        self.configobj[s][o] = self.defaults[s][o]
                    except IndexError:
                        self.configobj[s][o] =  ""
        # write the config file to a string
        with StringIO("") as s:
            self.configobj.write(s)
            s.seek(0)
            cfgstr = s.read()
        # add the descriptions as comments
        for s in self.descriptions.keys():
            cfgstr = cfgstr.replace("["+s+"]","\n# "+self.descriptions[s]["_section"]+"\n"+"["+s+"]")
            # also add a note on default (if available) and comment out the option
            for o in self.descriptions[s].keys():
                try:
                    cfgstr = cfgstr.replace("\n"+o+" = ","\n\n# "+self.descriptions[s][o]+"\n# Default is: "+self.defaults[s][o]+"\n#"+o+" = ")
                except IndexError:
                    cfgstr = cfgstr.replace("\n"+o+" = ","\n\n# "+self.descriptions[s][o]+"\n#"+o+" = ")
        # print final config file
        print(cfgstr)
    _logger = None

    # configure the class
    def __init__(self,args_dict):
        import argparse
        import logging
        from logging import handlers
        # get any command line arguments
        parser = argparse.ArgumentParser()
        parser.add_argument("--config-file",default="/etc/opensips/mmsgate.conf",type=str,help="Load this config file.  Default is /etc/opensips/mmsgate.conf.")
        parser.add_argument("--default-values",default="",type=str,help="Additional/Override default config setting.  \"key1=val;key2=val;...\"")
        for opt,arg in args_dict:
            parser.add_argument(opt,**arg)
        args = parser.parse_args()
        # need to print default config w/ descriptions?
        if hasattr(args,"default_config") and args.default_config:
            self.print_default_config()
            exit()
        # additional defaults from command line
        if hasattr(args,"default_values") and "=" in args.default_values:
            self.defaults['cmdline'] = {}
            for kv in args.default_values.split(";"):
                k,v = kv.split("=",1)
                self.defaults['cmdline'][k] = v
        # load the config file
        self.load(args.config_file)
        # setup logger
        date_fmt = '%Y-%m-%d %H:%M:%S'
        try:
            if hasattr(args,"mmsgate_logger") and args.mmsgate_logger != "":
                self.loglvltext = args.mmsgate_logger
            else:
                self.loglvltext = self.get("mmsgate","logger")
            loglvl = self.loglevels[self.loglvltext]
        except Exception as e:
            raise ValueError("Error: Bad MMSGate logging level. "+str(e))
        if self.exists("mmsgate","loggerfile"):
            log_format = "%(levelname)s %(asctime)s.%(msecs)03d %(process)d %(threadName)s %(filename)s.%(funcName)s %(message)s"
            logging.basicConfig(format=log_format, datefmt=date_fmt, level=loglvl, filename=os.path.expanduser(self.get("mmsgate","loggerfile")))
            print("Logging to",self.get("mmsgate","loggerfile"))
        else:
            log_format = "%(levelname)s %(process)d %(threadName)s %(filename)s.%(funcName)s %(message)s"
            logging.basicConfig(format=log_format, level=loglvl, handlers=[handlers.SysLogHandler(address = '/dev/log', facility='local6')])
        self._logger = logging.getLogger(__name__)
        # override the config file"s debug levels with command options
        if hasattr(args,"pjsip_debug") and args.pjsip_debug != -1:
            self.configobj["sip"]["siploglevel"] = str(args.pjsip_debug)
            self.configobj["sip"]["sipconsoleloglevel"] = str(args.pjsip_debug)
        self.args = args

# this is a new process that runs all the non-gunicorn threads
def threads(ask_q,resp_q,loglvl_q):
    import signal
    import threading
    threading.current_thread().name = "THREADS-MANAGER"

    # this is a thread to respond to multiprocessing queue requests from gunicorn processes
    def thread2process_q(ask_q,resp_q,loglvl_q):
        did_accts = {}
        # loop forever
        while True:
            i = ask_q.get()
            if i[0] == "GetLogLevel":
                loglvl_q.put_nowait(_logger.level)
            if i[0] == "SetLogLevel":
                _logger.debug("thread2process_q: "+str(i))
                _logger.setLevel(i[1])
            if i[0] == "GetAccts":
                _logger.debug("thread2process_q: "+str(i))
                resp_q.put_nowait(did_accts)
            if i[0] == "PutAccts":
                _logger.debug("thread2process_q: "+str(i))
                did_accts = i[1]
            if i[0] == "MsgNew":
                _logger.debug("thread2process_q: "+str(i))
                db.db_q.put_nowait(i)

    # setup up the db thread
    db = db_class()
    # start thread
    db.start()

    # start thread for gunicorn requests
    wt = threading.Thread(name="WEB2THREADS" , target=thread2process_q, args=(ask_q,resp_q,loglvl_q), daemon=True)
    wt.start()
    _logger.debug("threads started")

    # init the did_accts for 1st time
    try:
        conn = get_dbconn()
        if conn is None:
            raise Exception("MMSGate db connection failed.")
        # create table/indexes if needed
        init_linphonedb(conn)
        init_subacctdb(conn)
        get_did_accts(conn,ask_q)
    except Exception as e:
        _logger.error("Error for first db subaccts init: "+str(e))

    # catch ctrl-c and term signals for graceful shutdown
    def signal_handler(signum, frame):
        _logger.warning("Received Signal Number: "+str(signum))
        sys.exit(0)
    signal.signal(signal.SIGTERM,signal_handler)
    signal.signal(signal.SIGINT,signal_handler)

    # watch the threads.  if one exists, shutdown...
    try:
        while True:
            for t in (wt,db.t):
                if not t.is_alive():
                    _logger.error("Thread "+t.name+" has ended.")
                    raise
            time.sleep(1)
    except Exception as e:
        _logger.error("Ending thread monitor: "+str(e))
    # tell DB and other threads to exit
    for q in (db.db_q,):
        q.put_nowait(("Done",))
    _logger.warning("Exiting MMSGate in 5 seconds...")
    time.sleep(5)
    os.kill(os.getppid(), signal.SIGTERM)

#
# main()
#
if __name__ == "__main__":
    import multiprocessing as mp

    # configure everything
    args = [("--default-config",{"action":"store_true","help":"Print a default config file and exit."}),
      ("--mmsgate-logger",{"default":"","type":str,"help":"Override the MMSGate log levels from the configuration file. Value is DEBUG, INFO, WARNING, ERROR or CRITICAL."})]
    cfg = config_class(args)
    _logger = cfg._logger

    # need a process to run all the threads for non-gunicorn processing
    ctx = mp.get_context('fork')
    ask_q = mp.Queue()
    resp_q = mp.Queue()
    loglvl_q = mp.Queue()
    p = ctx.Process(target=threads,args=(ask_q,resp_q,loglvl_q),daemon=True)
    p.start()
    _logger.debug("Threads process started - PID: "+str(p.pid))

    # setup the web class with gunicorn
    web = web_class(ask_q,resp_q,loglvl_q)
    # clean up memory (fork will dup all the current process memory)
    import gc
    gc.collect()
    # let gunicorn take over
    web.start()
