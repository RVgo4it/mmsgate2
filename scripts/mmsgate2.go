/*
MMSGate, a MMS gateway between OpenSIPS and VoIP.ms for use by Linphone clients.
Copyright (C) 2026 by RVgo4it, https://github.com/RVgo4it
Permission to use, copy, modify, and/or distribute this software for any purpose with or without
fee is hereby granted.
THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES WITH REGARD TO THIS
SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE
AUTHOR BE LIABLE FOR ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN ACTION OF CONTRACT,
NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR PERFORMANCE
OF THIS SOFTWARE.
*/

// v1.1.8 3/1/2026 Disable DNS failover in opensips.cfg and other minor bugs
// v1.1.7 2/28/2026 Switch to sipexer due to bug in opensips-cli
// v1.1.6 2/27/2026 Fixed crontab delete from send_msgs
// v1.1.5 2/26/2026 Enhanced messages when changing log/xlog levels for OpenSIPS
// v1.1.4 2/25/2026 Build fixes for low memory hosts - works on host w/ 250m free memory
// v1.1.3 2/13/2026 Turn off PN in client config when no Linphone account
// v1.1.2 2/11/2026 Added UUID to vcard contacts
// v1.1.1 2/7/2026 Switched to PN via REST
// v1.1.0 1/22/2026 Replaced Python scripts with Go program - 3 fold memory reduction
// v1.0.13 1/5/2026 Fixes to client config section
// v1.0.12 1/4/2026 Applied ruff fixes
// v1.0.11 1/3/2026 New python3-multipart required rewrite upload service plus crontab enhancements
// v1.0.10 1/2/2026 Minor bug fixes and Wizard enhancements
// v1.0.9 12/26/2025 Improved firewall testing in wazard
// v1.0.8 12/24/2025 Minor bugs and wizard fixes.
// v1.0.7 12/5/2025 Added password change for /admin and minor bugs
// v1.0.6 12/3/2025 Added fix for /admin password and 32 vs 64 /usr
// v1.0.5 12/2/2025 Added more Windows support
// v1.0.4 11/28/2025 Bug fix in wizard
// v1.0.3 11/27/2025 Extra security for admin page
// v1.0.2 11/27/2025 Minor fixes in wizard and Voip.ms sub acct admin
// v1.0.1 11/26/2025 Switched to OpenSIPS v3.6, added check in FW test and added OpenSIPS auto_scaling_profile
// v1.0.0 11/19/2025 Major rewrite for OpenSIPS and Push Notification via linphone.org

// go mod init main
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"html/template"
	"io"
	"log/syslog"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/icholy/digest"
	_ "github.com/mattn/go-sqlite3"

	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unsafe"

	qrcode "github.com/skip2/go-qrcode"
)

/*
 * This is for rsyslog logging
 */
type myLogger struct {
	gsysLog       *syslog.Writer
	gsysLogPri    syslog.Priority
	gsysLogMap    *map[string]syslog.Priority
	gsysLogMapRev map[syslog.Priority]string
}

/*
 * myLogger: Initialize logger
 */
func (l *myLogger) init(loglvl syslog.Priority) *myLogger {
	var err error
	l.gsysLogPri = loglvl
	l.str2lvl("init")
	l.gsysLog, err = syslog.Dial("", "",
		l.gsysLogPri|syslog.LOG_LOCAL6, "mmsgate2")
	if err != nil {
		panic(err)
	}
	return l
}

/*
 * myLogger: change the log level
 */
func (l *myLogger) setlvl(loglvl syslog.Priority) *myLogger {
	l.gsysLogPri = loglvl
	return l
}

/*
 * myLogger: log a message
 */
func (l *myLogger) mylog(sev syslog.Priority, m string) *myLogger {
	msg := l.gsysLogMapRev[sev] + ": " + m
	if sev <= l.gsysLogPri {
		switch sev {
		case syslog.LOG_EMERG:
			l.gsysLog.Emerg(msg)
		case syslog.LOG_ALERT:
			l.gsysLog.Alert(msg)
		case syslog.LOG_CRIT:
			l.gsysLog.Crit(msg)
		case syslog.LOG_ERR:
			l.gsysLog.Err(msg)
		case syslog.LOG_WARNING:
			l.gsysLog.Warning(msg)
		case syslog.LOG_NOTICE:
			l.gsysLog.Notice(msg)
		case syslog.LOG_INFO:
			l.gsysLog.Info(msg)
		case syslog.LOG_DEBUG:
			l.gsysLog.Debug(msg)
		}
	}
	if l.gsysLogPri == syslog.LOG_DEBUG && os.Getenv("DEBUGCON") == "Y" {
		fmt.Printf("%s mmsgate2: %s: %s\n", time.Now().Format(time.RFC3339), l.gsysLogMapRev[sev], m)
	}
	return l
}

/*
 * myLogger: lookup the log level from a string
 */
func (l *myLogger) str2lvl(lvl string) (syslog.Priority, error) {
	var lvlmap = map[string]syslog.Priority{
		"EMERG":   syslog.LOG_EMERG,
		"ALERT":   syslog.LOG_ALERT,
		"CRIT":    syslog.LOG_CRIT,
		"ERR":     syslog.LOG_ERR,
		"WARNING": syslog.LOG_WARNING,
		"NOTICE":  syslog.LOG_NOTICE,
		"INFO":    syslog.LOG_INFO,
		"DEBUG":   syslog.LOG_DEBUG,
	}
	if l != nil && l.gsysLogMap == nil {
		l.gsysLogMap = &lvlmap
		l.gsysLogMapRev = make(map[syslog.Priority]string, len(*l.gsysLogMap))
		for k, v := range *l.gsysLogMap {
			l.gsysLogMapRev[v] = k
		}
	}
	lvlv, ok := lvlmap[lvl]
	if ok {
		return lvlv, nil
	} else {
		return syslog.LOG_WARNING, errors.New("Invalid log level")
	}
}

/*
 * This is for connecting to the DB
 */
func get_dbconn() *sql.DB {
	// get file path from ENV
	sdb := os.Getenv("DBPATHM")
	ml.mylog(syslog.LOG_DEBUG, "DB Path: "+sdb)
	db, err := sql.Open("sqlite3", "file:"+sdb+"?cache=shared&mode=rwc&_loc=auto")
	// open ok?
	if err != nil {
		ml.mylog(syslog.LOG_EMERG, "Error opensing DB: "+sdb+" : "+err.Error())
		// can't go on
		panic(errors.New("DB Open Error: " + sdb + " : " + err.Error()))
	} else {
		db.SetMaxOpenConns(1)
		ml.mylog(syslog.LOG_DEBUG, "Opened DB: "+sdb)
		return db
	}
}

/*
 * Setup Linphone table
 */
func init_linphonedb() {
	// build tables if needed
	_, err := db.Exec("CREATE TABLE IF NOT EXISTS linphone (username TEXT UNIQUE, password TEXT, account_creation_request_token TEXT, account_creation_token TEXT, " +
		"provisioning_token TEXT, captcha_url TEXT, activated INT DEFAULT 0, email TEXT, domain TEXT DEFAULT 'sip.linphone.org', apikey TEXT);")
	if err != nil {
		ml.mylog(syslog.LOG_EMERG, "Error creating DB table: "+err.Error())
		// can't go on
		panic(errors.New("Error creating DB table: " + err.Error()))
	}
	_, err = db.Exec("CREATE INDEX IF NOT EXISTS lp_user ON linphone (username);")
	if err != nil {
		ml.mylog(syslog.LOG_EMERG, "Error creating DB index: "+err.Error())
		// can't go on
		panic(errors.New("Error creating DB index: " + err.Error()))
	}
}

/*
 * Setup sub account table
 */
func init_subacctdb() {
	// build tables if needed
	_, err := db.Exec("CREATE TABLE IF NOT EXISTS subacct (account TEXT UNIQUE, password TEXT, callerid TEXT, ext TEXT, smsmms INT DEFAULT 1, " +
		"linphone TEXT, uuid TEXT, tls TEXT, max_expiry TEXT, internal_cnam TEXT, description TEXT, ip TEXT, domain TEXT);")
	if err != nil {
		ml.mylog(syslog.LOG_EMERG, "Error creating DB table: "+err.Error())
		// can't go on
		panic(errors.New("Error creating DB table: " + err.Error()))
	}
	_, err = db.Exec("CREATE INDEX IF NOT EXISTS sa_ip ON subacct (ip);")
	if err != nil {
		ml.mylog(syslog.LOG_EMERG, "Error creating DB index: "+err.Error())
		// can't go on
		panic(errors.New("Error creating DB index: " + err.Error()))
	}
	_, err = db.Exec("CREATE INDEX IF NOT EXISTS sa_acct ON subacct (account);")
	if err != nil {
		ml.mylog(syslog.LOG_EMERG, "Error creating DB index: "+err.Error())
		// can't go on
		panic(errors.New("Error creating DB index: " + err.Error()))
	}
	_, err = db.Exec("CREATE INDEX IF NOT EXISTS sa_linacct ON subacct (linphone,account);")
	if err != nil {
		ml.mylog(syslog.LOG_EMERG, "Error creating DB index: "+err.Error())
		// can't go on
		panic(errors.New("Error creating DB index: " + err.Error()))
	}
}

/*
 * message queue table for incoming from webhook
 */
func init_msgdb() {
	_, err := db.Exec("CREATE TABLE IF NOT EXISTS send_msgs (rcvd_ts INT DEFAULT (strftime('%s', 'now')), fromid TEXT, toid TEXT, fromdom TEXT, todom TEXT, msgtype TEXT, " +
		"did TEXT, direction TEXT, message TEXT, msgstatus TEXT DEFAULT 'QUEUED', sent_ts INT DEFAULT 0, init_ts INT DEFAULT (strftime('%s', 'now')), trycnt INT DEFAULT 0, msgid TEXT);")
	if err != nil {
		ml.mylog(syslog.LOG_EMERG, "Error creating DB table: "+err.Error())
		// can't go on
		panic(errors.New("Error creating DB table: " + err.Error()))
	}
	// indexes for selects and updates.  partial indexes are restricted to queued/active messages.
	_, err = db.Exec("CREATE INDEX IF NOT EXISTS sm_to ON send_msgs (toid,todom,rcvd_ts) WHERE msgstatus NOT IN ('200','202');")
	if err != nil {
		ml.mylog(syslog.LOG_EMERG, "Error creating DB index: "+err.Error())
		// can't go on
		panic(errors.New("Error creating DB index: " + err.Error()))
	}
}

/*
 * Call a VoIP.ms API
 */
func voip_api(url string) ([]byte, error) {
	ml.mylog(syslog.LOG_DEBUG, "VoIP.ms API URL: "+url)
	// make the API call
	resp, err := http.Get(url)
	if err != nil {
		return nil, errors.New("VoIP.ms's API call failed: " + err.Error())
	}
	// read in the entire response
	body, err := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if err != nil {
		return body, errors.New("VoIP.ms's API call failed: " + err.Error())
	}
	ml.mylog(syslog.LOG_DEBUG, "VoIP.ms API response body: "+string(body))
	// parse the status message from the JSON response
	type jresp struct {
		Status string
	}
	var oresp jresp
	err = json.Unmarshal(body, &oresp)
	if err != nil {
		return body, errors.New("VoIP.ms's API call failed, parse result: " + err.Error())
	}
	if oresp.Status != "success" {
		return body, errors.New("VoIP.ms's API call failed, status: " + oresp.Status)
	}
	// everything ok
	return body, nil
}

/*
 * Update the sub account DB
 */
func pop_subacctdb() (msg []string) {
	defer func() {
		if err := recover(); err != nil {
			strerr := HandleErrorWithLines(err.(error))
			msg = append(msg, "Panic: "+strerr)
		}
	}()
	// need these params
	apiid := os.Getenv("APIID")
	apipw := os.Getenv("APIPW")
	dnsname := os.Getenv("DNSNAME")
	// build messages to return
	msg = []string{}
	// got what we need?
	if apiid == "" || apipw == "" || dnsname == "" {
		msg = append(msg, "Missing ENV VAR APIID, APIPW or DNSNAME.  Cannot populate sub account db.")
		ml.mylog(syslog.LOG_ERR, "Missing ENV VAR APIID, APIPW or DNSNAME.  Cannot populate sub account db.")
		return
	}
	// webhook path needed for did setting at https://voip.ms
	protocol := os.Getenv("PROTOCOL")
	webport := os.Getenv("WEBPORT")
	pathgate := os.Getenv("PATHGATE")
	webpostpath := protocol + "://" + dnsname + ":" + webport + pathgate
	ml.mylog(syslog.LOG_DEBUG, "Web hook path: "+webpostpath)
	// collect a list/map of DIDs
	didsmap := map[string]string{}
	// get DIDs
	body, err := voip_api(fmt.Sprintf("https://voip.ms/api/v1/rest.php?api_username=%s&api_password=%s&method=getDIDsInfo", apiid, url.QueryEscape(apipw)))
	if err != nil {
		msg = append(msg, "Failed call to getDIDsInfo: "+err.Error())
		ml.mylog(syslog.LOG_ERR, "Failed call to getDIDsInfo: "+err.Error())
		return
	}
	ml.mylog(syslog.LOG_DEBUG, "VoIP.ms API getDIDsInfo: "+string(body))
	// parse the JSON
	type did struct {
		Did             string
		Description     string
		Webhook         string
		Webhook_enabled string
		Sms_enabled     string
		Pop             string
	}
	type dids struct {
		Dids []did
	}
	var odids dids
	err = json.Unmarshal(body, &odids)
	if err != nil {
		msg = append(msg, "VoIP.ms's API call failed, dids parse result: "+err.Error())
		ml.mylog(syslog.LOG_ERR, "VoIP.ms's API call failed, dids parse result: "+err.Error())
		return
	}
	// loop each DID
	for _, did := range odids.Dids {
		// store it later so we can confirm CID for sub acct
		didsmap[did.Did] = did.Description
		// check that web hook correct for this DID
		if did.Webhook != webpostpath || did.Webhook_enabled != "1" {
			msg = append(msg, "Please enable 'SMS/MMS Webhook URL' for DID "+did.Did+" and set it to "+webpostpath)
			ml.mylog(syslog.LOG_WARNING, "Please enable 'SMS/MMS Webhook URL' for DID "+did.Did+" and set it to "+webpostpath)
		}
		// is messaging enabled for this DID?
		if did.Sms_enabled != "1" {
			msg = append(msg, "Please enable 'Message Service (SMS/MMS)' for DID "+did.Did)
			ml.mylog(syslog.LOG_WARNING, "Please enable 'Message Service (SMS/MMS)' for DID "+did.Did)
		}
	}
	// drop temp table for initial sub acct load
	_, err = db.Exec("DROP TABLE IF EXISTS tsubacct;")
	if err != nil {
		msg = append(msg, "Failed drop temp table: "+err.Error())
		ml.mylog(syslog.LOG_ERR, "Failed drop temp table: "+err.Error())
		return
	}
	// temp table for initial sub acct load
	_, err = db.Exec("CREATE TEMPORARY TABLE tsubacct (account TEXT UNIQUE, password TEXT, callerid TEXT, ext TEXT, tls TEXT, max_expiry TEXT, internal_cnam TEXT, description TEXT);")
	if err != nil {
		msg = append(msg, "Failed create temp table: "+err.Error())
		ml.mylog(syslog.LOG_ERR, "Failed create temp table: "+err.Error())
		return
	}
	// get sub accts
	body, err = voip_api(fmt.Sprintf("https://voip.ms/api/v1/rest.php?api_username=%s&api_password=%s&method=getSubAccounts", apiid, url.QueryEscape(apipw)))
	if err != nil {
		msg = append(msg, "Failed call to getSubAccounts: "+err.Error())
		ml.mylog(syslog.LOG_ERR, "Failed call to getSubAccounts: "+err.Error())
		return
	}
	ml.mylog(syslog.LOG_DEBUG, "VoIP.ms API getSubAccounts: "+string(body))
	// parse the JSON result
	type account struct {
		Account            string
		Internal_extension string
		Callerid_number    string
		Password           string
		// sometimes the API returns a floating point number and not a string.  we'll accept either (any).
		Sip_traffic   any
		Max_expiry    string
		Internal_cnam string
		Description   string
	}
	type accts struct {
		Accounts []account
	}
	var oaccts accts
	err = json.Unmarshal(body, &oaccts)
	if err != nil {
		msg = append(msg, "VoIP.ms's API call failed, accts parse result: "+err.Error())
		ml.mylog(syslog.LOG_ERR, "VoIP.ms's API call failed, accts parse result"+err.Error())
		return
	}
	// loop each sub acct
	rows := int64(0)
	for _, acct := range oaccts.Accounts {
		// we store the "10" prefix in the db
		if acct.Internal_extension != "" {
			acct.Internal_extension = "10" + acct.Internal_extension
		}
		// if CID not a valid DID, ignore it
		if _, ok := didsmap[acct.Callerid_number]; !ok {
			acct.Callerid_number = ""
		}
		// insert into temp DB
		sqlrslt, err := db.Exec("INSERT INTO tsubacct (account,password,callerid,ext,tls,max_expiry,internal_cnam,description) VALUES(?,?,?,?,?,?,?,?)",
			acct.Account, acct.Password, acct.Callerid_number, acct.Internal_extension, acct.Sip_traffic, acct.Max_expiry, acct.Internal_cnam, acct.Description)
		if err != nil {
			msg = append(msg, "Insert into temp acct DB table failed: "+err.Error())
			ml.mylog(syslog.LOG_ERR, "Insert into temp acct DB table failed: "+err.Error())
			return
		}
		// keep count
		row, err := sqlrslt.RowsAffected()
		rows += row
	}
	ml.mylog(syslog.LOG_DEBUG, "Inserted rows into temp acct DB: "+fmt.Sprint(rows))
	// append any new sub accts
	_, err = db.Exec("INSERT OR IGNORE INTO subacct (account,password,callerid,ext,tls,max_expiry) SELECT account,password,callerid,ext,tls,max_expiry FROM tsubacct;")
	if err != nil {
		msg = append(msg, "Insert into sub acct DB table failed: "+err.Error())
		ml.mylog(syslog.LOG_ERR, "Insert into sub acct DB table failed: "+err.Error())
		return
	}
	// delete any sub accts that don't exist any more at voip.ms
	_, err = db.Exec("DELETE FROM subacct WHERE account NOT IN (SELECT account FROM tsubacct);")
	if err != nil {
		msg = append(msg, "Delete from sub acct DB table failed: "+err.Error())
		ml.mylog(syslog.LOG_ERR, "Delete from sub acct DB table failed: "+err.Error())
		return
	}
	// update the values that may have change at voip.ms.  cast tls due to api returning a string or float
	_, err = db.Exec("UPDATE subacct SET password = t.password, callerid = t.callerid, ext = t.ext, tls = CAST(t.tls AS INTEGER), max_expiry = t.max_expiry, " +
		"internal_cnam = t.internal_cnam, description = t.description FROM (SELECT * FROM tsubacct) AS t WHERE subacct.account = t.account;")
	if err != nil {
		msg = append(msg, "Update sub acct DB table failed: "+err.Error())
		ml.mylog(syslog.LOG_ERR, "Update sub acct DB table failed: "+err.Error())
		return
	}
	acctrows, err := query2map("SELECT account FROM subacct WHERE uuid IS NULL;")
	if err != nil {
		msg = append(msg, "Query missing UUID sub accts failed: "+err.Error())
		ml.mylog(syslog.LOG_ERR, "Query missing UUID sub accts failed: "+err.Error())
		return
	}
	for _, row := range acctrows {
		// make one for unique path
		uuid := uuid.New().String()
		_, err = db.Exec("UPDATE subacct SET uuid = ? WHERE account=?;", uuid, row["account"].(string))
		if err != nil {
			ml.mylog(syslog.LOG_ERR, "SQL query for configmenu/uuid update: "+err.Error())
			msg = append(msg, "Error SQL query for uuid update: "+err.Error())
		}
	}
	// done
	ml.mylog(syslog.LOG_DEBUG, "Result msg: "+strings.Join(msg, "\n"))
	return
}

/*
 * this is used for MMS messages
 */
const mms_template = `<?xml version="1.0" encoding="UTF-8"?>
<file xmlns="urn:gsma:params:xml:ns:rcs:rcs:fthttp" xmlns:am="urn:gsma:params:xml:ns:rcs:rcs:rram">
<file-info type="file">
<file-size>%d</file-size>
<file-name>%s</file-name>
<content-type>%s</content-type>
<data url="%s" until=%q/>
</file-info>
</file>`

/*
 * File upload from linphone app
 */
func uploadHandler(w http.ResponseWriter, r *http.Request) {
	// if panic,print/log details
	defer func() {
		if err := recover(); err != nil {
			HandleErrorWithLines(err.(error))
		}
	}()
	switch r.Method {
	// GET displays the upload form.
	case "GET":
		ml.mylog(syslog.LOG_WARNING, "GET method for /file")
	// POST takes the uploaded file(s) and saves it to disk.
	case "POST":
		ml.mylog(syslog.LOG_DEBUG, "POST method for /file")
		// for unique path
		uuid := uuid.New()
		// for the xml response
		fname := ""
		fsize := int64(0)
		furl := ""
		proto := os.Getenv("PROTOCOL")
		dnsname := os.Getenv("DNSNAME")
		webport := os.Getenv("WEBPORT")
		localmedia := os.Getenv("LOCALMEDIA")
		pathget := os.Getenv("PATHGET")
		// defaults for message
		mimetype := "application/binary"
		until := time.Now().Add(time.Duration(-365) * time.Hour * 24).Format(time.RFC3339)
		// parse the multipart form in the request
		err := r.ParseMultipartForm(100000)
		if err != nil {
			http.Error(w, "No Content", http.StatusNoContent)
			ml.mylog(syslog.LOG_DEBUG, "Error ParseMultipartForm: "+err.Error())
			return
		}
		// get a ref to the parsed multipart form
		m := r.MultipartForm
		for f := range m.File {
			ml.mylog(syslog.LOG_DEBUG, "multipart name: "+f)
			files := m.File[f]
			for i := range files {
				// for each fileheader, get a handle to the actual file
				file, err := files[i].Open()
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					ml.mylog(syslog.LOG_ERR, "Error ParseMultipartForm open reader: "+err.Error())
					return
				}
				defer file.Close()
				// create destination file making sure the path is writeable.
				ml.mylog(syslog.LOG_DEBUG, "Upload filename: "+files[i].Filename)
				fname = files[i].Filename
				dname := localmedia + "/" + uuid.String()
				fpath := dname + "/" + fname
				furl = proto + "://" + dnsname + ":" + webport + pathget + "/" + uuid.String() + "/" + fname
				err = os.MkdirAll(dname, 0777)
				if err != nil {
					ml.mylog(syslog.LOG_ERR, "Error creating path: ("+dname+"): "+err.Error())
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				dst, err := os.Create(fpath)
				if err != nil {
					ml.mylog(syslog.LOG_ERR, "Error writing to file from POST: ("+fpath+"): "+err.Error())
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				defer dst.Close()
				// write the uploaded file to the destination file
				if fsize, err = io.Copy(dst, file); err != nil {
					ml.mylog(syslog.LOG_ERR, "Error ParseMultipartForm saving to disk: "+err.Error())
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				out, err := exec.Command("file", "--mime-type", "-b", fpath).Output()
				if err != nil {
					ml.mylog(syslog.LOG_WARNING, "Failed to lookup mime type: ("+fpath+"): "+err.Error())
				} else {
					mimetype = strings.Replace(string(out), "\n", "", -1)
					ml.mylog(syslog.LOG_DEBUG, "Mime type for '"+fpath+"' is "+string(out))
				}
			}

		}
		if fname != "" {
			xml := fmt.Sprintf(mms_template, fsize, fname, mimetype, furl, until)
			ml.mylog(syslog.LOG_DEBUG, "XML Response: "+xml)
			// display success message.
			w.Write([]byte(xml))
			ml.mylog(syslog.LOG_DEBUG, "Upload successful.")
		} else {
			// no file?
			http.Error(w, "No Content", http.StatusNoContent)
			ml.mylog(syslog.LOG_DEBUG, "Error no file found.")
			return
		}
	default:
		ml.mylog(syslog.LOG_WARNING, "Method to /file not a GET or POST: "+r.Method)
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

/*
 * web hook from VoIP.ms
 */
func webhookHandler(w http.ResponseWriter, r *http.Request) {
	// if panic,print/log details
	defer func() {
		if err := recover(); err != nil {
			HandleErrorWithLines(err.(error))
		}
	}()
	switch r.Method {
	// GET displays nothing
	case "GET":
		ml.mylog(syslog.LOG_WARNING, "GET method for /mmsgate")
		w.WriteHeader(http.StatusMethodNotAllowed)
	// POST incoming message
	case "POST":
		ml.mylog(syslog.LOG_DEBUG, "POST method for /mmsgate")
		// struct for decoding json
		type addr struct {
			Phone_number string
		}
		type media struct {
			Url string
		}
		type payload struct {
			Id          uint64
			Record_type string
			From        addr
			To          []addr
			Text        string
			Received_at string
			Type        string
			Media       []media
		}
		type data struct {
			Id      uint64
			Payload payload
		}
		type msg struct {
			Data data
		}
		// read entire POST-ed data
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			ml.mylog(syslog.LOG_ERR, "VoIP.ms's webhook body read error: "+err.Error())
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		ml.mylog(syslog.LOG_DEBUG, "VoIP.ms's webhook body: "+string(body))
		// decode the JSON
		var webhook msg
		err = json.Unmarshal(body, &webhook)
		if err != nil {
			ml.mylog(syslog.LOG_ERR, "VoIP.ms's webhook decode error: "+err.Error())
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// keep track if no sub accts to send to
		noinsert := true
		// loop for each "to" DID found in json
		for _, to := range webhook.Data.Payload.To {
			// got one to send
			ml.mylog(syslog.LOG_DEBUG, "Processing To: "+to.Phone_number)
			// get all sub accts for this DID
			toacctarr, err := get_tosubaccts(to.Phone_number)
			if err != nil {
				ml.mylog(syslog.LOG_ERR, "Error get_tosubaccts: "+err.Error())
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			// loop for each sub acct from array built from db
			for _, thisacct := range toacctarr {
				ml.mylog(syslog.LOG_DEBUG, "Sending to sub acct: "+thisacct.acct)
				// check text part first (common for SMS)
				if webhook.Data.Payload.Text != "" {
					ml.mylog(syslog.LOG_DEBUG, "Sending text message")
					// queue it
					err = insert_msg(webhook.Data.Payload.From.Phone_number, thisacct.domain, thisacct.acct, thisacct.domain,
						webhook.Data.Payload.Text, to.Phone_number, "SMS", strconv.FormatUint(webhook.Data.Id, 10), "")
					if err != nil {
						ml.mylog(syslog.LOG_ERR, "insert_msg returned error: "+err.Error())
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					// don't need a dummy record
					noinsert = false
				}
				// check each media url (common for MMS)
				for _, murl := range webhook.Data.Payload.Media {
					ml.mylog(syslog.LOG_DEBUG, "Found medua url: "+murl.Url)
					// need new MMS XML from URL
					xml, err := url2xml(murl.Url)
					if err != nil {
						ml.mylog(syslog.LOG_ERR, "Error converting URL to XML: ("+murl.Url+"): "+err.Error())
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
					ml.mylog(syslog.LOG_DEBUG, "XML Message: "+xml)
					// queue it
					err = insert_msg(webhook.Data.Payload.From.Phone_number, thisacct.domain, thisacct.acct, thisacct.domain,
						xml, to.Phone_number, webhook.Data.Payload.Type, strconv.FormatUint(webhook.Data.Id, 10), "")
					if err != nil {
						ml.mylog(syslog.LOG_ERR, "Error from insert_msg: "+err.Error())
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					// don't need a dummy record
					noinsert = false
				}
			}
		}
		// Just to know we receive this message ID for when we reconcile
		if noinsert {
			insert_msg(webhook.Data.Payload.From.Phone_number, "dummy", "any", "dummy",
				webhook.Data.Payload.Text, webhook.Data.Payload.To[0].Phone_number, "SMS", strconv.FormatUint(webhook.Data.Id, 10), "202")
			ml.mylog(syslog.LOG_DEBUG, "Dummy msg record inserted: MSG ID="+strconv.FormatUint(webhook.Data.Id, 10))
		}
		// bump the send queue
		c <- true
		// all done
		w.WriteHeader(http.StatusOK)
	default:
		ml.mylog(syslog.LOG_WARNING, "Method to /mmsgate not a GET or POST: "+r.Method)
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

/*
 * converts URL for MMS media into XML for MMS message
 */
func url2xml(url string) (string, error) {
	// need these if doing MMS
	proto := os.Getenv("PROTOCOL")
	dnsname := os.Getenv("DNSNAME")
	webport := os.Getenv("WEBPORT")
	localmedia := os.Getenv("LOCALMEDIA")
	pathget := os.Getenv("PATHGET")
	// need to download it
	uuid := uuid.New()
	fname := filepath.Base(url)
	dname := localmedia + "/" + uuid.String()
	fpath := dname + "/" + fname
	furl := proto + "://" + dnsname + ":" + webport + pathget + "/" + uuid.String() + "/" + fname
	ml.mylog(syslog.LOG_DEBUG, "New medua url: "+furl)
	mimetype := "application/binary"
	err := os.MkdirAll(dname, 0777)
	if err != nil {
		ml.mylog(syslog.LOG_ERR, "Error creating path: ("+dname+"): "+err.Error())
		return "", err
	}
	out, err := os.Create(fpath)
	if err != nil {
		ml.mylog(syslog.LOG_ERR, "Create file failed: ("+fpath+") "+err.Error())
		return "", err
	}
	defer out.Close()
	resp, err := http.Get(url)
	if err != nil {
		ml.mylog(syslog.LOG_ERR, "VoIP.ms's API call failed: "+err.Error())
		return "", err
	}
	defer resp.Body.Close()
	fsize, err := io.Copy(out, resp.Body)
	// now can get mime type
	cmdout, err := exec.Command("file", "--mime-type", "-b", fpath).Output()
	if err != nil {
		ml.mylog(syslog.LOG_WARNING, "Failed to lookup mime type: ("+fpath+"): "+err.Error())
	} else {
		mimetype = strings.ReplaceAll(string(cmdout), "\n", "")
		ml.mylog(syslog.LOG_DEBUG, "Mime type for '"+fpath+"' is "+string(cmdout))
	}
	// build MMS XML
	until := time.Now().Add(time.Duration(90) * time.Hour * 24).Format(time.RFC3339)
	return fmt.Sprintf(mms_template, fsize, fname, mimetype, furl, until), nil
}

/*
 * this is for returning list of sub accts to forward a message sent to a DID
 */
type toacct struct {
	acct   string
	domain string
}

/*
 * lookup and return sub accts to send SMS/MMS message received by DID/CID
 */
func get_tosubaccts(to string) ([]toacct, error) {
	// look up sub accts w/ caller ID matching receiving DID
	rows, err := db.Query("SELECT account, domain FROM subacct WHERE domain IS NOT NULL AND domain != '' AND smsmms = 1 AND callerid = ?;",
		to)
	if err != nil {
		ml.mylog(syslog.LOG_ERR, "SQL error getting accts for msg: "+err.Error())
		return nil, errors.New("SQL error getting accts for msg: " + err.Error())
	}
	defer rows.Close()
	// loop each receiving sub acct and put in map
	toacctarr := []toacct{}
	for rows.Next() {
		var thisacct toacct
		err = rows.Scan(&thisacct.acct, &thisacct.domain)
		ml.mylog(syslog.LOG_DEBUG, "Found route mapping: "+to+"->"+thisacct.acct)
		if err != nil {
			ml.mylog(syslog.LOG_ERR, "SQL error scanning acct for msg: "+err.Error())
			return nil, errors.New("SQL error scanning acct for msg: " + err.Error())
		}
		toacctarr = append(toacctarr, thisacct)
	}
	rows.Close()
	return toacctarr, nil
}

/*
 * insert a message into DB queue
 */
func insert_msg(fromid string, fromdom string, toid string, todom string, message string, did string, msgtype string, msgid string, msgstat string) error {
	// insert
	rslt, err := db.Exec("INSERT INTO send_msgs(fromid,fromdom,toid,todom,message,direction,did,msgtype,msgid,msgstatus) VALUES(?,?,?,?,?,'IN',?,?,?,?);",
		fromid, fromdom, toid, todom, message, did, msgtype, msgid, msgstat)
	if err != nil {
		ml.mylog(syslog.LOG_ERR, "SQL error inserting into send_msgs: "+err.Error())
		return err
	}
	// get row ID...  just for debug message
	id, err := rslt.LastInsertId()
	if err == nil {
		ml.mylog(syslog.LOG_DEBUG, "SQL inserted into send_msgs: ID="+strconv.FormatInt(id, 10))
	}
	// no error
	return nil
}

/*
 * this sends messages to OpenSIPS from the database queue.
 */
func send_msgs(c chan bool) {
	// query record
	type rec struct {
		rcvd_ts   sql.NullInt64
		sent_ts   sql.NullInt64
		fromid    sql.NullString
		fromdom   sql.NullString
		toid      sql.NullString
		todom     sql.NullString
		message   sql.NullString
		msgstatus sql.NullString
		did       sql.NullString
		rowid     sql.NullInt64
		msgtype   sql.NullString
		trycnt    sql.NullInt64
	}
	// loop forever! maybe...
	for {
		// keep looping?
		res := true
		select {
		// channel value can bump loop
		case res = <-c:
			ml.mylog(syslog.LOG_DEBUG, "Got value from channel: "+strconv.FormatBool(res))
		// if no bump, will still loop every minute
		case <-time.After(1 * time.Minute):
			res = true
		}
		// if not, break loop
		if !res {
			break
		}
		// keep looping until no more pending msgs
		for {
			// assume nothing pending
			morepending := false
			// select pending messages, i.e. status not 200/202.  group by toid and min(rowid) to just see the oldest message for each toid
			// also, skip messages we already tried within 10 minutes
			sql_select_pending :=
				"SELECT rcvd_ts,sent_ts,fromid,fromdom,toid,todom,message,msgstatus,did,rowid,msgtype,trycnt " +
					"FROM (" +
					"    SELECT *,min(rowid) AS rowid " +
					"    FROM send_msgs " +
					"    WHERE msgstatus NOT IN ('200','202') " +
					"    GROUP BY toid) " +
					"WHERE sent_ts < CAST(strftime('%s',datetime('now','-10 minutes')) AS INTEGER) " +
					"ORDER BY rowid;"
			rows, err := db.Query(sql_select_pending)
			if err != nil {
				ml.mylog(syslog.LOG_ERR, "SQL error getting pending msgs: "+err.Error())
				continue
			}
			defer rows.Close()
			// loop each receiving sub acct and put in array
			recs := []rec{}
			for rows.Next() {
				var thisrec rec
				err = rows.Scan(&thisrec.rcvd_ts, &thisrec.sent_ts, &thisrec.fromid, &thisrec.fromdom, &thisrec.toid, &thisrec.todom, &thisrec.message,
					&thisrec.msgstatus, &thisrec.did, &thisrec.rowid, &thisrec.msgtype, &thisrec.trycnt)
				if err != nil {
					ml.mylog(syslog.LOG_ERR, "SQL error scanning pending msgs: "+err.Error())
					continue
				}
				recs = append(recs, thisrec)
			}
			// done w/ cursor
			rows.Close()
			// loop the slice array of records
			for _, thisrec := range recs {
				// found stuff... so we'll come back here again
				morepending = true
				// timestamps for printing
				sent := time.Unix(thisrec.sent_ts.Int64, 0).Local()
				rcvd := time.Unix(thisrec.rcvd_ts.Int64, 0).Local()
				ml.mylog(syslog.LOG_DEBUG, "Found pending to msg: "+fmt.Sprintf("Msg received: %v\nLast send try: %v\n%+v\n", rcvd, sent, thisrec))
				// note the cmi result
				clirslt := ""
				// determine the SIP MESSAGE content type
				var msgtype string
				if thisrec.msgtype.String == "SMS" {
					msgtype = "text/plain"
				} else {
					msgtype = "application/vnd.gsma.rcs-ft-http+xml"
				}
				// send SIP MESSAGE via OpenSIPS-CLI management interface (MI)
				//cmdout, err := exec.Command("opensips-cli", "-x", "mi", "t_uac_dlg", "method=MESSAGE", fmt.Sprintf("ruri=sips:%s@%s;transport=tls", thisrec.toid.String, thisrec.todom.String),
				//	fmt.Sprintf("next_hop=sips:%s", "localhost"),
				//	fmt.Sprintf("headers=To: sips:%s@%s\\r\\nFrom: sips:%s@%s\\r\\nContent-Type: %s\\r\\n", thisrec.toid.String, thisrec.todom.String, thisrec.fromid.String, thisrec.fromdom.String, msgtype),
				//	fmt.Sprintf("body=%s", thisrec.message.String)).Output()
				//if err != nil {
				//	ml.mylog(syslog.LOG_WARNING, "Failed to send message via OpenSIPS CLI: "+err.Error())
				//	clirslt = err.Error()
				//} else {
				//	ml.mylog(syslog.LOG_DEBUG, "OpenSIPS CLI result: "+string(cmdout))
				//	// parse the status message from the JSON response
				//	type jresp struct {
				//		Status string
				//	}
				//	var oresp jresp
				//	err = json.Unmarshal(cmdout, &oresp)
				//	if err != nil {
				//		ml.mylog(syslog.LOG_WARNING, "Failed to parse results from OpenSIPS CLI: "+err.Error())
				//		clirslt = err.Error()
				//	} else {
				//		// 200/202 are a success!
				//		if oresp.Status == "200 Ok" || oresp.Status == "202 Accepted" {
				//			clirslt = oresp.Status[:3]
				//		} else {
				//			clirslt = oresp.Status
				//		}
				//	}
				//}
				// use sipexer to send message.  opensips-cli having issues...
				cmd := exec.Command("/scripts/sipexer", "-message", "-xh", "Content-Type: "+msgtype,
					"-to-uri", fmt.Sprintf("sips:%s@%s", thisrec.toid.String, thisrec.todom.String),
					"-from-uri", fmt.Sprintf("sips:%s@%s", thisrec.fromid.String, thisrec.fromdom.String),
					"-ruri", fmt.Sprintf("sips:%s@%s", thisrec.toid.String, thisrec.todom.String),
					"-mb", thisrec.message.String,
					"udp:127.0.0.1:5060")
				out, err := cmd.Output()
				exitcode := cmd.ProcessState.ExitCode()
				clirslt = strconv.Itoa(exitcode)
				n := bytes.IndexByte(out[:], 0)
				if exitcode != 200 && exitcode != 202 {
					ml.mylog(syslog.LOG_ERR, "sipexec: "+strings.Join(cmd.Args, " ")+"\nExitCode = "+clirslt+"\n "+string(out[:n]))
				} else {
					ml.mylog(syslog.LOG_DEBUG, "sipexec: "+strings.Join(cmd.Args, " ")+"\nExitCode = "+clirslt+"\n "+string(out[:n]))
				}
				// update the message status in the db
				sql_update_status_via_rowid := "UPDATE send_msgs SET sent_ts = CAST(strftime('%s', 'now') AS INTEGER),msgstatus = ?, trycnt = trycnt + 1 WHERE rowid = ?;"
				rslt, err := db.Exec(sql_update_status_via_rowid, clirslt, thisrec.rowid)
				if err != nil {
					ml.mylog(syslog.LOG_EMERG, "Error updating row ID "+strconv.FormatInt(thisrec.rowid.Int64, 10)+" :"+err.Error())
					continue
				}
				// to confirm
				rows, err := rslt.RowsAffected()
				if err == nil {
					ml.mylog(syslog.LOG_DEBUG, "SQL update send_msgs rows: "+strconv.FormatInt(rows, 10))
				}
			}
			// if none in this loop, break to outer loop to sleep 1 minute (or get bumped)
			if !morepending {
				break
			}
		}
	}
}

/*
 * Check old message at VoIP.ms against the DB.  if any missing, resend.
 */
func reconcile() error {
	// we need these for the API call
	apiid := os.Getenv("APIID")
	apipw := os.Getenv("APIPW")
	if apiid == "" || apipw == "" {
		return errors.New("Error: ENV VAR APIID and APIPW are required.")
	}
	// need this for how far back to look
	recodays, err := strconv.ParseInt(os.Getenv("RECODAYS"), 10, 64)
	if err != nil {
		ml.mylog(syslog.LOG_WARNING, "Bad environment variable 'RECODAYS'.  Defaulting to 7 days look back for reconciles.")
		recodays = 7
	}
	if recodays > 30 || recodays < 1 {
		ml.mylog(syslog.LOG_WARNING, "Environment variable 'RECODAYS' not 1 - 30.  Defaulting to 7 days look back for reconciles.")
		recodays = 7
	}
	// look back date
	since := time.Now().Add(time.Duration(-recodays) * time.Hour * 24).Format(time.DateOnly)
	// API call to query old messages
	body, err := voip_api(fmt.Sprintf("https://voip.ms/api/v1/rest.php?api_username=%s&api_password=%s&method=getMMS&type=1&from=%s&all_messages=1&did=%s",
		apiid, url.QueryEscape(apipw), since, ""))
	if err != nil {
		ml.mylog(syslog.LOG_ERR, "Failed call to getMMS: "+err.Error())
		return errors.New("Failed call to getMMS")
	}
	ml.mylog(syslog.LOG_DEBUG, "VoIP.ms API getMMS: "+string(body))
	// for json parse
	type Sms struct {
		Id             string
		Date           string
		Type           string
		Did            string
		Contact        string
		Carrier_status string
		Message        string
		col_media1     string
		col_media2     string
		col_media3     string
		Media          []string
	}
	type mms struct {
		Status string
		Sms    []Sms
	}
	var rcvd mms
	err = json.Unmarshal(body, &rcvd)
	if err != nil {
		ml.mylog(syslog.LOG_ERR, "VoIP.ms's API call failed, getMMS parse result: "+err.Error())
		return errors.New("Failed to parse getMMS")
	}
	// loop each message returned from API call
	for _, sms := range rcvd.Sms {
		// already done?
		var count int64
		err := db.QueryRow("SELECT COUNT(rowid) AS msg_count FROM send_msgs WHERE msgid = ?;", sms.Id).Scan(&count)
		if err != nil {
			ml.mylog(syslog.LOG_ERR, "Error query count of msgid: "+err.Error())
			return err
		}
		// no record of this message...  send now
		if count == 0 {
			// keep track if we sent it
			noinsert := true
			toacctarr, err := get_tosubaccts(sms.Did)
			if err != nil {
				ml.mylog(syslog.LOG_ERR, "Error get_tosubaccts: "+err.Error())
				return err
			}
			for _, thisacct := range toacctarr {
				if sms.Message != "" {
					ml.mylog(syslog.LOG_DEBUG, "Sending SMS text message: "+sms.Id)
					err = insert_msg(sms.Contact, thisacct.domain, thisacct.acct, thisacct.domain,
						sms.Message, sms.Did, "SMS", sms.Id, "")
					if err != nil {
						ml.mylog(syslog.LOG_ERR, "insert_msg returned error: "+err.Error())
						return err
					}
					noinsert = false
				}
				for _, url := range sms.Media {
					xml, err := url2xml(url)
					if err != nil {
						ml.mylog(syslog.LOG_ERR, "Func url2xml returned: "+err.Error())
						return err
					}
					ml.mylog(syslog.LOG_DEBUG, "Sending MMS message: "+sms.Id)
					err = insert_msg(sms.Contact, thisacct.domain, thisacct.acct, thisacct.domain,
						xml, sms.Did, "MMS", sms.Id, "")
					if err != nil {
						ml.mylog(syslog.LOG_ERR, "insert_msg returned error: "+err.Error())
						return err
					}
					noinsert = false
				}
			}
			// Just to know we already received this message ID for when we reconcile again
			if noinsert {
				insert_msg(sms.Contact, "dummy", "any", "dummy",
					sms.Message, sms.Did, "SMS", sms.Id, "202")
				ml.mylog(syslog.LOG_DEBUG, "Dummy msg record inserted: MSG ID="+sms.Id)
			}
		}
	}
	return nil
}

/*
 * This will loop and run reconcile every RECOHRS hours
 */
func sched_reconcile() {
	// need this for how often to reconcile
	recohrs, err := strconv.ParseInt(os.Getenv("RECOHRS"), 10, 64)
	if err != nil {
		ml.mylog(syslog.LOG_WARNING, "Bad environment variable 'RECOHRS'.  Defaulting to 6 hours between reconciles.")
		recohrs = 6
	}
	if recohrs > 24 || recohrs < 1 {
		ml.mylog(syslog.LOG_WARNING, "Environment variable 'RECOHRS'. 'RECOHRS' not 1 - 24.  Defaulting to 6 hours between reconciles.")
		recohrs = 6
	}
	for {
		err = reconcile()
		if err != nil {
			ml.mylog(syslog.LOG_ERR, "Reconcile of messages failed: "+err.Error())
		}
		// sleep for RECOHRS hours
		time.Sleep(time.Duration(recohrs) * time.Hour)
	}
}

/*
 * admin web interface
 */
func adminHandler(w http.ResponseWriter, r *http.Request) {
	// if panic,print/log details
	defer func() {
		if err := recover(); err != nil {
			HandleErrorWithLines(err.(error))
		}
	}()
	err := r.ParseForm()
	if err != nil {
		ml.mylog(syslog.LOG_ERR, "Failed to parse form")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// check for log request
	if r.Form.Get("log") != "" {
		// open the log fifo file
		logfifo := "/tmp/fifodir/" + r.Form.Get("log")
		f, err := os.OpenFile(logfifo, os.O_RDONLY, 0644)
		// open okay?
		if err == nil {
			// close later
			defer func() {
				if err := f.Close(); err != nil {
					ml.mylog(syslog.LOG_ERR, "Error closing fifo file ["+logfifo+"]: "+err.Error())
				}
			}()
			// read 1k bytes
			buf := make([]byte, 1024)
			// keep looping until no new log lines
			for {
				// w/o waiting
				f.SetReadDeadline(time.Now().Add(time.Duration(1) * time.Millisecond))
				n, err := f.Read(buf)
				if err != nil {
					if !errors.Is(err, os.ErrDeadlineExceeded) {
						ml.mylog(syslog.LOG_ERR, "Error reading from ["+logfifo+"]: "+err.Error())
					}
				}
				// write them to the javascript app running in the browser
				if n > 0 {
					w.Write(buf[:n])
				}
				// caught up?
				if n < 1024 {
					break
				}
			}
		} else {
			// open failed, tell the javascript app running in the browser that we are done
			w.Write(([]byte)("--DONE--"))
		}
		return
	}
	ml.mylog(syslog.LOG_DEBUG, "Handler for admin: "+r.Method)
	// may need client browser type
	r.Form["useragent"] = []string{r.Header.Get("User-Agent")}
	// log the form
	var s = ""
	for k, v := range r.Form {
		s = s + fmt.Sprintf("'%s'='%s'\n", k, v[0])
	}
	ml.mylog(syslog.LOG_DEBUG, "Parsed form and URL query: \n"+s)

	// defaults if missing in form
	if r.Form.Get("nextpage") == "" {
		r.Form["nextpage"] = []string{"mainmenu"}
	}
	if r.Form.Get("thispage") == "" {
		r.Form["thispage"] = []string{"mainmenu"}
	}
	// for splitting the form name at "-" is there is one...
	formsplit := func(form string) (string, string) {
		a := strings.Split(form+"-", "-")
		return a[0], a[1]
	}
	wizi := 1
	// look at the form submitted - this is mostly a router depending on form fields
	submitedform, submitedsubform := formsplit(r.Form.Get("thispage"))
	switch submitedform {
	case "mainmenu":
		// buttons pressed and where to go
		for _, mm := range []struct{ name, menu string }{
			{"Linphone", "linmenu"},
			{"Voip.ms", "voipmsmenu"},
			{"Wizard", "wizmenu-1"},
			{"Advanced", "advmenu"}} {
			for _, fv := range r.Form {
				if fv[0] == mm.name {
					r.Form["nextpage"] = []string{mm.menu}
					ml.mylog(syslog.LOG_DEBUG, "Set 'nextpage' to: "+mm.menu)
				}
			}
		}
	case "advmenu":
		// buttons pressed and where to go
		for _, mm := range []struct{ name, menu string }{
			{"Exit", "mainmenu"},
			{"Enable_OpenSIPS", "advmenu-enableopensips"},
			{"Disable_OpenSIPS", "advmenu-disableopensips"},
			{"Stop_OpenSIPS", "advmenu-stopopensips"},
			{"Adjust_Log_Level", "advmenu-setloglevel"},
			{"Adjust_xLog_Level", "advmenu-setxloglevel"},
			{"Restart_Container", "advmenu-restart"},
			{"Set_Global_DEBUG", "advmenu-setglobaldebug"},
			{"Adjust_MMSGate_Log_Level", "advmenu-setmmsgateloglevel"},
			{"Set_Admin_Password", "advmenu-setadminpassword"},
			{"Display_SQLite_data", "advmenu-dumpdatabase"},
			{"Display_Live_Logs", "advmenu-displaylivelogs"}} {
			for _, fv := range r.Form {
				if fv[0] == mm.name {
					r.Form["nextpage"] = []string{mm.menu}
					ml.mylog(syslog.LOG_DEBUG, "Set 'nextpage' to: "+mm.menu)
				}
			}
			// for custom adv sub, cancel for back to adv menu
			if r.Form.Get("button2") == "Cancel" {
				r.Form["nextpage"] = []string{"advmenu"}
			}
		}
	case "configmenu":
		// cancel in config is back to voip.ms form
		if r.Form.Get("button2") == "Cancel" {
			r.Form["nextpage"] = []string{"voipmsmenu"}
		}
	case "voipmsmenu":
		// click cancel?  return to main menu
		if r.Form.Get("button2") == "Cancel" {
			r.Form["nextpage"] = []string{"mainmenu"}
		}
		// check each row for click config client
		for i := 0; ; i += 1 {
			si := strconv.Itoa(i)
			// no more rows?
			username := r.Form.Get("username-" + si)
			if username == "" {
				break
			}
			// clicked it?
			if r.Form.Get("client_config-"+si) == "Client Config" {
				// click sub acct becomes 1st row on client config
				r.Form["cfg-username-0"] = []string{username}
				// route to config page
				r.Form["nextpage"] = []string{"configmenu"}
			}
		}
	case "wizmenu":
		// any sub menu selected?  for wiz, it's a number
		wizi, err = strconv.Atoi(submitedsubform)
		if err != nil {
			wizi = 1
		}
		// hit cancel to go back?
		if r.Form.Get("button1") == "Back" {
			// on first wiz page?
			if wizi == 1 {
				r.Form["nextpage"] = []string{"mainmenu"}
				// just back one page
			} else {
				r.Form["nextpage"] = []string{"wizmenu-" + strconv.Itoa(wizi-1)}
			}
		}
	case "linmenu":
		// cancel?  back to main menu
		if r.Form.Get("button2") == "Cancel" {
			r.Form["nextpage"] = []string{"mainmenu"}
		}
	default:
		r.Form["nextpage"] = []string{"mainmenu"}
	}
	// build form to display
	type m struct {
		MenuName, MenuDesc string
	}
	data := dat{false, false, "Refresh", "Cancel", "", "mainmenu", "mainmenu", "", []any{}, []template.HTML{}}
	var tmplname string
	// generate the form to display
	// displayform := strings.Split(r.Form.Get("nextpage"), "-")[0]
	displayform, displaysubform := formsplit(r.Form.Get("nextpage"))
	switch displayform {
	case "mainmenu":
		// build items for main menu display form
		data.CustomItems = []any{m{"Wizard", "Step by step configuration of MMSGate"},
			m{"Linphone", "Manage Linphone accounts for push notifications - Add/Edit/Delete"},
			m{"Voip.ms", "Configure Voip.ms Accounts for MMSGate and configure clients"},
			m{"Advanced", "Advanced menu for logs, restarts, etc."}}
		tmplname = "form-table-menu"
	case "advmenu":
		// map of func to call for sub adv menu
		type menu func(url.Values, *dat) (string, error)
		submenumap := map[string]menu{"dumpdatabase": sqldump,
			"enableopensips":     enableopensips,
			"disableopensips":    disableopensips,
			"setloglevel":        setloglevel,
			"setxloglevel":       setxloglevel,
			"displaylivelogs":    livelogs,
			"stopopensips":       stopopensips,
			"setmmsgateloglevel": setgateloglvl,
			"setglobaldebug":     setglobaldebug,
			"setadminpassword":   setadminpassword,
			"restart":            restart}
		// build items for adv menu display form - could be overwritten by custom adv sub form
		data.Nextpage = "advmenu"
		data.Thispage = "advmenu"
		data.Title2 = " - Advanced"
		data.CustomItems = []any{
			m{"Enable_OpenSIPS", "To allow OpenSIPS to start within 1 minute."},
			m{"Disable_OpenSIPS", "Prevent OpenSIPS from restarting if stopped."},
			m{"Stop_OpenSIPS", "Stop OpenSIPS and if not disabled, restart it."},
			m{"Adjust_Log_Level", "Change the OpenSIPS script log level written to log file."},
			m{"Adjust_xLog_Level", "Change the OpenSIPS script xlog level written to log file."},
			m{"Adjust_MMSGate_Log_Level", "Change the MMSGate script log level written to log file."},
			m{"Set_Global_DEBUG", "Change the Global DEBUG settings used by the system's BASH scripts."},
			m{"Restart_Container", "Stop this container and if configured as such in Docker, restart it."},
			m{"Display_Live_Logs", "Display live logs in real time.  (Not compatible with Lynx.)"},
			m{"Display_SQLite_data", "Dump the database tables used for MMSGate."},
			m{"Set_Admin_Password", "Set the admin password for MMSGate."},
			m{"Exit", "Return to main menu"}}
		// call sub adv menu
		if f, ok := submenumap[displaysubform]; ok {
			tmplname, err = f(r.Form, &data)
			if err != nil {
				ml.mylog(syslog.LOG_ERR, "Sub menu '"+displayform+"' returned error: "+err.Error())
			}
			// if no sub adv menu, just default to adv menu
		} else {
			tmplname = "form-table-menu"
		}
	case "configmenu":
		tmplname, err = configmenu(r.Form, &data)
		if err != nil {
			ml.mylog(syslog.LOG_ERR, "Config menu returned error: "+err.Error())
		}
	case "voipmsmenu":
		tmplname, err = voipmsmenu(r.Form, &data)
		if err != nil {
			ml.mylog(syslog.LOG_ERR, "VoIP.ms menu returned error: "+err.Error())
		}
	case "wizmenu":
		wizi, err = strconv.Atoi(displaysubform)
		if err != nil {
			wizi = 1
		}
		tmplname, err = wizard(r.Form, &data, wizi)
		if err != nil {
			ml.mylog(syslog.LOG_ERR, "Wizard menu returned error: "+err.Error())
		}
	case "linmenu":
		tmplname, err = linmenu(r.Form, &data)
		if err != nil {
			ml.mylog(syslog.LOG_ERR, "Linphone menu returned error: "+err.Error())
		}
	default:
	}
	// write form using template
	t, ok := tmpl[tmplname]
	if ok {
		err = t.ExecuteTemplate(w, "base", data)
		if err != nil {
			ml.mylog(syslog.LOG_ERR, "Error from ExecuteTemplate: "+err.Error())
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			// w.WriteHeader(http.StatusOK)
		}
	} else {
		ml.mylog(syslog.LOG_ERR, "Error: No or bad template name: "+tmplname)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

/*
 * used for form data
 */
type dat struct {
	Btn1, Btn2                   bool
	Btn1val, Btn2val             string
	Title2                       string
	Nextpage, Thispage, Passdata string
	CustomItems                  []any
	Msgs                         []template.HTML
}

/*
 * Query the DB and return array of row maps
 */
func query2map(query string, args ...any) (retmap []map[string]any, reterr error) {
	retmap = []map[string]any{}
	// if panic,print/log details
	defer func() {
		if err := recover(); err != nil {
			strerr := HandleErrorWithLines(err.(error))
			reterr = errors.New(strerr)
		}
	}()
	rows, err := db.Query(query, args...)
	if err != nil {
		ml.mylog(syslog.LOG_ERR, "SQL error 'query2map' Query: "+err.Error())
		reterr = err
		return
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		ml.mylog(syslog.LOG_ERR, "SQL error 'query2map' Columns: "+err.Error())
		reterr = err
		return
	}
	for rows.Next() {
		r := map[string]any{}
		pointers := make([]any, len(cols))
		for i := range cols {
			var v any
			pointers[i] = &v
		}
		err = rows.Scan(pointers...)
		for i, col := range cols {
			p, ok := pointers[i].(*any)
			if ok {
				r[col] = *p
			} else {
				ml.mylog(syslog.LOG_ERR, "SQL error 'query2map' convert: "+err.Error())
				reterr = err
				return
			}
		}
		if err != nil {
			ml.mylog(syslog.LOG_ERR, "SQL error 'query2map' Scan: "+err.Error())
			reterr = err
			return
		}
		retmap = append(retmap, r)
	}
	rows.Close()
	return
}

/*
 * Query the DB and return array of row arrays
 */
func query2array(query string, args ...any) (retarr [][]any, reterr error) {
	// if panic,print/log details
	defer func() {
		if err := recover(); err != nil {
			strerr := HandleErrorWithLines(err.(error))
			reterr = errors.New(strerr)
		}
	}()
	retarr = [][]any{}
	rows, err := db.Query(query, args...)
	if err != nil {
		ml.mylog(syslog.LOG_ERR, "SQL error 'query2array' Query: "+err.Error())
		reterr = err
		return
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		ml.mylog(syslog.LOG_ERR, "SQL error 'query2array' Columns: "+err.Error())
		reterr = err
		return
	}
	for rows.Next() {
		r := make([]any, len(cols))
		pointers := make([]any, len(cols))
		for i := range cols {
			pointers[i] = &r[i]
		}
		err = rows.Scan(pointers...)
		if err != nil {
			ml.mylog(syslog.LOG_ERR, "SQL error 'query2array' Scan: "+err.Error())
			reterr = err
			return
		}
		retarr = append(retarr, r)
	}
	rows.Close()
	return
}

/*
 * execute a template from tmpl map w/ data and return result as HTML string
 */
func template2html(tmplname string, data any) (template.HTML, error) {
	buf := new(bytes.Buffer)
	err := tmpl[tmplname].Execute(buf, data)
	if err != nil {
		return template.HTML(""), err
	}
	return template.HTML(buf.String()), nil
}

/*
 * generate the VoIP.ms sub acct form and perform updates
 */
func voipmsmenu(Form url.Values, data *dat) (retfrorm string, reterr error) {
	retfrorm = "form-table-2d"
	// if panic,print/log details
	defer func() {
		if err := recover(); err != nil {
			strerr := HandleErrorWithLines(err.(error))
			reterr = errors.New(strerr)
		}
	}()
	// some initial form values
	data.CustomItems = []any{}
	data.Btn1 = true
	data.Btn2 = true
	data.Thispage = "voipmsmenu"
	data.Nextpage = "voipmsmenu"
	data.Title2 = " - Manage Voip.ms Sub Accounts"
	// need VoIP.ms changes?
	changesneeded := false
	// first time here? or hit refresh?
	if Form.Get("thispage") != data.Thispage || Form.Get("button1") == "Refresh" {
		// get fresh sub accts from voip.ms
		msg := pop_subacctdb()
		// pass along any messages
		for _, m := range msg {
			changesneeded = true
			ml.mylog(syslog.LOG_WARNING, "Msg from populate sub acct func: "+m)
			data.Msgs = append(data.Msgs, template.HTML(m))
		}
	}
	// search through form data for action to perform
	for k, v := range Form {
		// hit the apply button? (update sms/mms and linphone acct)
		if v[0] == "Apply" {
			// need the index to get other values
			ka := strings.Split(k, "-")
			if len(ka) > 1 {
				// index for other keys
				i := ka[1]
				// get the other values to update
				username := Form.Get("username-" + i)
				// linphone field in da is null or valid acct
				var linphone sql.NullString
				linphone.String = Form.Get("linphone-" + i)
				if linphone.String == "N/A" {
					linphone.Valid = false
				} else {
					linphone.Valid = true
				}
				// sms/mms is 0 for ignore, 1 for accept
				smsmms := 0
				if Form.Get("smsmms-"+i) == "Accept" {
					smsmms = 1
				}
				// valid data?
				if username != "" && linphone.String != "" {
					// update!
					_, err := db.Exec("UPDATE subacct SET linphone = ?, smsmms = ? WHERE account = ?;", linphone, smsmms, username)
					ml.mylog(syslog.LOG_DEBUG, "Updating subacct table: "+username)
					data.Msgs = append(data.Msgs, template.HTML("Updated sub account "+username))
					if err != nil {
						ml.mylog(syslog.LOG_WARNING, "Error updating subacct table: "+err.Error())
						data.Msgs = append(data.Msgs, template.HTML("Error updating subacct table: : "+err.Error()))
					}
				} else {
					ml.mylog(syslog.LOG_WARNING, "Error parsing form username/linphone missing.")
					data.Msgs = append(data.Msgs, template.HTML("Error parsing form username/linphone missing"))
				}
			} else {
				ml.mylog(syslog.LOG_WARNING, "Error parsing form keys: "+k+"="+v[0])
				data.Msgs = append(data.Msgs, template.HTML("Error Error parsing form keys: "+k+"="+v[0]))
			}
		}
	}
	// get list of linphone accts for select tag
	linphonearr, err := query2array("SELECT 'N/A' UNION SELECT username FROM linphone;")
	if err != nil {
		ml.mylog(syslog.LOG_ERR, "Getting linphone accts for voipmsmenu: "+err.Error())
		data.Msgs = append(data.Msgs, template.HTML("Error getting linphone accts: "+err.Error()))
	}
	// get sub accts for form list
	query := "SELECT account, callerid, CASE smsmms WHEN 0 THEN 'Ignore' ELSE 'Accept' END AS smsmms, IFNULL(linphone, 'N/A') AS linphone, " +
		"CASE ext WHEN '' THEN 'null' ELSE ext END AS ext, CASE tls WHEN 0 THEN 'N/A' ELSE 'TLS' END AS tls, internal_cnam, description FROM subacct ORDER BY callerid,account;"
	rows, err := query2map(query)
	if err != nil {
		ml.mylog(syslog.LOG_ERR, "Getting accts for voipmsmenu: "+err.Error())
		data.Msgs = append(data.Msgs, template.HTML("Error getting accts: "+err.Error()))
	}
	// header row
	data.CustomItems = append(data.CustomItems, struct {
		Header bool
		Row    []any
	}{true, []any{"Sub Account", "", "DID/CallerID", "SMS/MMS", "Push Notif", "", "Extension", "Internal Cnam", "Encryption", "Description", "", "Note"}})
	// loop each receiving sub acct and add row to form
	for i, row := range rows {
		si := strconv.Itoa(i)
		// tag to get the acct name later when apply hit
		accounthiddentag := template.HTML(fmt.Sprintf("<input id='username-%d' name='username-%d' type='hidden' value='%s'>", i, i, row["account"]))
		// submit tag to apply new values
		applytag := template.HTML(fmt.Sprintf("<input id='apply-%d' name='apply-%d' type='submit' value='Apply'>", i, i))
		// select tag for enable/disable sms/mms
		smsmmsselecttag, err := template2html("selecttag", struct {
			Name  string
			Value any
			Rows  [][]any
		}{"smsmms-" + si, row["smsmms"], [][]any{{"Ignore"}, {"Accept"}}})
		if err != nil {
			ml.mylog(syslog.LOG_ERR, "Getting html voipmsmenu/smsmms: "+err.Error())
			data.Msgs = append(data.Msgs, template.HTML("Error getting smsmms html: "+err.Error()))
		}
		// select tag to pick linphone acct for push notif
		linphoneselecttag, err := template2html("selecttag", struct {
			Name  string
			Value any
			Rows  [][]any
		}{"linphone-" + si, row["linphone"], linphonearr})
		if err != nil {
			ml.mylog(syslog.LOG_ERR, "Getting html voipmsmenu/linphone: "+err.Error())
			data.Msgs = append(data.Msgs, template.HTML("Error getting linphone html: "+err.Error()))
		}
		// assume no note to display
		note := ""
		// assume configtag not disabled
		disabled := ""
		// no encryption?  make note and disable config
		if row["tls"] == any("N/A") {
			changesneeded = true
			note += "Please enable 'Encrypted SIP Traffic' for sub account.  "
			disabled = "disabled"
		}
		// no extension?  make note and disable config
		if row["ext"] == any("null") {
			changesneeded = true
			note += "Please enter an 'Internal Extension Number' for sub account.  "
			disabled = "disabled"
		}
		// no caller id matching did?  make note and disable config
		if row["callerid"] == any("") {
			changesneeded = true
			note += "Please select a DID for 'CallerID Number' for sub account.  "
			disabled = "disabled"
		}
		// config button to config the client w/ this sub acct
		configtag := template.HTML(fmt.Sprintf("<input %s id='client_config-%d' name='client_config-%d' type='submit' value='Client Config'>", disabled, i, i))
		// append the row
		data.CustomItems = append(data.CustomItems, struct {
			Header bool
			Row    []any
		}{false, []any{row["account"], accounthiddentag, row["callerid"], smsmmsselecttag, linphoneselecttag,
			applytag, row["ext"], row["internal_cnam"], row["tls"], row["description"], configtag, note}})
	}
	if changesneeded {
		data.Msgs = append(data.Msgs, template.HTML("Please logon to <a href='https://voip.ms/' target='_blank'>VoIP.ms</a> and make the necessary changes."))
	}
	// return form name to process
	return
}

/*
 * generate client config form
 */
func configmenu(Form url.Values, data *dat) (retform string, reterr error) {
	retform = "form-table-2d"
	// if panic,print/log details
	defer func() {
		if err := recover(); err != nil {
			strerr := HandleErrorWithLines(err.(error))
			reterr = errors.New(strerr)
		}
	}()
	// some initial form values
	data.CustomItems = []any{}
	data.Btn1 = true
	data.Btn2 = true
	data.Thispage = "configmenu"
	data.Nextpage = "configmenu"
	data.Title2 = " - Client Config"
	// need these params
	apiid := os.Getenv("APIID")
	apipw := os.Getenv("APIPW")
	// contains the accts to configure - maybe includes linphone acct
	rows := []map[string]any{}
	// get the subacct to configure as a start
	subacct := Form.Get("cfg-username-" + "0")
	// need these later
	linphone := ""
	struuid := ""
	// get info on selected acct
	query := "SELECT account, password, callerid, uuid, linphone, max_expiry, '' AS domain FROM subacct WHERE account=?;"
	row, err := query2map(query, subacct)
	if err != nil {
		ml.mylog(syslog.LOG_ERR, "SQL query for configmenu/subacct[0]: "+err.Error())
		data.Msgs = append(data.Msgs, template.HTML("Error SQL query for sub acct[0]: "+err.Error()))
	} else {
		// append for acct
		rows = append(rows, row[0])
		// maybe linphone acct to use for push notification
		if row[0]["linphone"] != nil {
			linphone = row[0]["linphone"].(string)
		}
		// does it have a uuid for config files?
		if row[0]["uuid"] == nil {
			// make one for unique path
			objuuid := uuid.New()
			struuid = objuuid.String()
			_, err = db.Exec("UPDATE subacct SET uuid = ? WHERE account=?;", struuid, subacct)
			if err != nil {
				ml.mylog(syslog.LOG_ERR, "SQL query for configmenu/uuid update: "+err.Error())
				data.Msgs = append(data.Msgs, template.HTML("Error SQL query for uuid update: "+err.Error()))
			}
		} else {
			struuid = row[0]["uuid"].(string)
		}
	}
	// if linphone for push notification, load linphone acct and other associated accts
	if linphone != "" {
		query := "SELECT * FROM (SELECT username AS account, password, '' AS callerid, '' AS uuid, '' AS linphone, '31536000' AS max_expiry, domain FROM linphone WHERE username=? " +
			"UNION " +
			"SELECT account, password, callerid, uuid, linphone, max_expiry, '' AS domain FROM subacct WHERE account!=? AND linphone = ? ) ORDER BY account;"
		row, err := query2map(query, linphone, subacct, linphone)
		if err != nil {
			ml.mylog(syslog.LOG_ERR, "SQL query for configmenu/subacct[1+]: "+err.Error())
			data.Msgs = append(data.Msgs, template.HTML("Error SQL query for sub subacct[1+]: "+err.Error()))
		}
		// append them all to be configured
		for i := range row {
			rows = append(rows, row[i])
		}
	} else {
		// no linphone, so add more accts based on html form
		for i := 1; i < 10; i++ {
			si := strconv.Itoa(i)
			// get sub acct name from form
			subacctadd := strings.Trim(Form.Get("cfg-username-"+si), " ")
			if subacctadd != "" {
				// look it up
				query := "SELECT account, password, callerid, uuid, linphone, max_expiry, '' AS domain FROM subacct WHERE account=?;"
				row, err := query2map(query, subacctadd)
				if err != nil {
					ml.mylog(syslog.LOG_ERR, "SQL query for configmenu/subacct[0]: "+err.Error())
					data.Msgs = append(data.Msgs, template.HTML("Error SQL query for sub acct[0]: "+err.Error()))
				}
				// add it to row
				rows = append(rows, row[0])
			} else {
				// no more sub accts on form
				break
			}
		}
	}
	// need to get domain and other details for DID
	for i, row := range rows {
		si := strconv.Itoa(i)
		// assume pw (ha1) for everyone
		row["pwenc"] = "Encrypted"
		// skip if linphone
		if row["domain"] != any("sip.linphone.org") {
			// check the form
			domain := Form.Get("cfg-domain-" + si)
			diddesc := Form.Get("cfg-diddesc-" + si)
			popname := Form.Get("cfg-popname-" + si)
			pwenc := Form.Get("cfg-pwenc-" + si)
			if domain != "" && diddesc != "" && popname != "" && pwenc != "" {
				row["domain"] = any(domain)
				row["diddesc"] = any(diddesc)
				row["popname"] = any(popname)
				row["pwenc"] = any(pwenc)
			} else {
				// get DIDs
				bodydid, err := voip_api(fmt.Sprintf("https://voip.ms/api/v1/rest.php?api_username=%s&api_password=%s&method=getDIDsInfo&did=%s", apiid, url.QueryEscape(apipw), row["callerid"].(string)))
				if err != nil {
					ml.mylog(syslog.LOG_ERR, "API call for configmenu/getDIDsInfo: "+err.Error())
					data.Msgs = append(data.Msgs, template.HTML("Error API call for getDIDsInfo: "+err.Error()))
				}
				ml.mylog(syslog.LOG_DEBUG, "VoIP.ms API getDIDsInfo: "+string(bodydid))
				// parse the JSON
				type did struct {
					Did             string
					Description     string
					Webhook         string
					Webhook_enabled string
					Sms_enabled     string
					Pop             string
				}
				type dids struct {
					Dids []did
				}
				var odids dids
				err = json.Unmarshal(bodydid, &odids)
				if err != nil {
					ml.mylog(syslog.LOG_ERR, "API call for configmenu/getDIDsInfo/parse: "+err.Error())
					data.Msgs = append(data.Msgs, template.HTML("Error API call for getDIDsInfo/parse: "+err.Error()))
				}
				if len(odids.Dids) > 0 {
					// get server pop
					bodysrv, err := voip_api(fmt.Sprintf("https://voip.ms/api/v1/rest.php?api_username=%s&api_password=%s&method=getServersInfo&server_pop=%s", apiid, url.QueryEscape(apipw), odids.Dids[0].Pop))
					if err != nil {
						ml.mylog(syslog.LOG_ERR, "API call for configmenu/getServersInfo: "+err.Error())
						data.Msgs = append(data.Msgs, template.HTML("Error API call for getServersInfo: "+err.Error()))
					}
					ml.mylog(syslog.LOG_DEBUG, "VoIP.ms API getServersInfo: "+string(bodysrv))
					// parse the JSON
					type server struct {
						Server_name      string
						Server_shortname string
						Server_hostname  string
					}
					type servers struct {
						Servers []server
					}
					var oservers servers
					err = json.Unmarshal(bodysrv, &oservers)
					if err != nil {
						ml.mylog(syslog.LOG_ERR, "API call for configmenu/getServersInfo/parse: "+err.Error())
						data.Msgs = append(data.Msgs, template.HTML("Error API call for getServersInfo/parse: "+err.Error()))
					}
					if len(oservers.Servers) > 0 {
						row["domain"] = oservers.Servers[0].Server_hostname
						row["popname"] = oservers.Servers[0].Server_name
						row["diddesc"] = odids.Dids[0].Description
						_, err = db.Exec("UPDATE subacct SET domain = ? WHERE account=?;", oservers.Servers[0].Server_hostname, row["account"].(string))
						if err != nil {
							ml.mylog(syslog.LOG_ERR, "SQL query for configmenu/uuid update: "+err.Error())
							data.Msgs = append(data.Msgs, template.HTML("Error SQL query for uuid update: "+err.Error()))
						}
					} else {
						ml.mylog(syslog.LOG_ERR, "API call for configmenu/getServersInfo/parse: "+err.Error())
						data.Msgs = append(data.Msgs, template.HTML("Error API call for getServersInfo/parse: "+err.Error()))
					}
				} else {
					ml.mylog(syslog.LOG_ERR, "API call for configmenu/getDIDsInfo/parse: "+err.Error())
					data.Msgs = append(data.Msgs, template.HTML("Error API call for getDIDsInfo/parse: "+err.Error()))
				}
			}
		}
	}
	// need this to build query for adding accts on form
	acctin := "'dummy'"
	// header row
	data.CustomItems = append(data.CustomItems, struct {
		Header bool
		Row    []any
	}{true, []any{"SIP Account", "", "Domian/Server", "", "Password", "DID/CallerID", "DID description", "", "PoP name", ""}})
	// loop each receiving sub acct and add row to form
	for i, row := range rows {
		si := strconv.Itoa(i)
		// need this to build query for adding accts
		acctin += ", '" + row["account"].(string) + "'"
		// tag to get the acct name later when apply hit
		accounthiddentag := template.HTML(fmt.Sprintf("<input id='cfg-username-%d' name='cfg-username-%d' type='hidden' value='%s'>", i, i, row["account"]))
		// tag to get the domain later when apply hit
		domainhiddentag := template.HTML(fmt.Sprintf("<input id='cfg-domain-%d' name='cfg-domain-%d' type='hidden' value='%s'>", i, i, row["domain"]))
		// tag to get the did desc later when apply hit
		diddeschiddentag := template.HTML(fmt.Sprintf("<input id='cfg-diddesc-%d' name='cfg-diddesc-%d' type='hidden' value='%s'>", i, i, row["diddesc"]))
		// tag to get the pop name later when apply hit
		popnamehiddentag := template.HTML(fmt.Sprintf("<input id='cfg-popname-%d' name='cfg-popname-%d' type='hidden' value='%s'>", i, i, row["popname"]))
		// select tag for enable/disable sms/mms
		var pwencselecttag template.HTML
		if row["account"] == any(linphone) {
			pwencselecttag = template.HTML("Encrypted")
		} else {
			pwencselecttag, err = template2html("selecttag", struct {
				Name  string
				Value any
				Rows  [][]any
			}{"cfg-pwenc-" + si, Form.Get("cfg-pwenc-" + si), [][]any{{"Encrypted"}, {"Clear"}, {"None"}}})
			if err != nil {
				ml.mylog(syslog.LOG_ERR, "Getting html configmenu/pwenc: "+err.Error())
				data.Msgs = append(data.Msgs, template.HTML("Error getting pwenc html: "+err.Error()))
			}
		}
		// append the row
		data.CustomItems = append(data.CustomItems, struct {
			Header bool
			Row    []any
		}{false, []any{row["account"], accounthiddentag, row["domain"], domainhiddentag, pwencselecttag, row["callerid"],
			row["diddesc"], diddeschiddentag, row["popname"], popnamehiddentag}})
	}
	// add the select for adding more accounts to config
	if linphone == "" {
		query = "SELECT * FROM (SELECT ' ' as account UNION SELECT account FROM subacct " +
			"WHERE callerid != '' AND tls != 0 AND ext != '' AND linphone IS NULL AND account NOT IN (" + acctin + ") ) ORDER BY account;"
		addaccts, err := query2array(query)
		if err != nil {
			ml.mylog(syslog.LOG_ERR, "SQL query for configmenu/addacct[n+]: "+err.Error())
			data.Msgs = append(data.Msgs, template.HTML("Error SQL query for sub addacct[n+]: "+err.Error()))
		} else {
			si := strconv.Itoa(len(rows))
			addselecttag, err := template2html("selecttag", struct {
				Name  string
				Value any
				Rows  [][]any
			}{"cfg-username-" + si, " ", addaccts})
			if err != nil {
				ml.mylog(syslog.LOG_ERR, "Getting html configmenu/addselecttag: "+err.Error())
				data.Msgs = append(data.Msgs, template.HTML("Error getting addselecttag html: "+err.Error()))
			} else {
				// append the row
				data.CustomItems = append(data.CustomItems, struct {
					Header bool
					Row    []any
				}{false, []any{addselecttag}})
			}
		}

	}
	// button to generate config
	data.CustomItems = append(data.CustomItems, struct {
		Header bool
		Row    []any
	}{false, []any{template.HTML("<input id='gen' name='gen' type='submit' value='Generate Config'>")}})
	// for client install
	clienturl := "https://www.linphone.org/en/download/#linphoneapp"
	data.Msgs = append(data.Msgs, template.HTML("Please install client from: <a href='"+clienturl+"' target='_blank'>"+clienturl+"</a>"))
	// was button pressed?
	if Form.Get("gen") == "Generate Config" {
		err := gen_config(rows, data, struuid, linphone)
		if err != nil {
			ml.mylog(syslog.LOG_ERR, "Config generate error: "+err.Error())
			data.Msgs = append(data.Msgs, template.HTML("Config generate error: "+err.Error()))
		}
	} else {
		// check for prev config
		localmedia := os.Getenv("LOCALMEDIA")
		dname := localmedia + "/" + struuid
		if _, err := os.Stat(dname); err == nil {
			// may need these
			proto := os.Getenv("PROTOCOL")
			dnsname := os.Getenv("DNSNAME")
			webport := os.Getenv("WEBPORT")
			pathget := os.Getenv("PATHGET")
			// check for the vcard file
			vcardname := dname + "/contacts.vcard"
			if fi, err := os.Stat(vcardname); err == nil {
				vcardurl := proto + "://" + dnsname + ":" + webport + pathget + "/" + struuid + "/contacts.vcard"
				data.Msgs = append(data.Msgs, template.HTML("Found vcard contacts list from "+fi.ModTime().Local().Format(time.ANSIC)+" for including in XML config: <a href='"+vcardurl+"'>contacts.vcard</a>"))
			}
			xmlname := dname + "/" + rows[0]["account"].(string) + ".xml"
			if fi, err := os.Stat(xmlname); err == nil {
				xmlurl := proto + "://" + dnsname + ":" + webport + pathget + "/" + struuid + "/" + rows[0]["account"].(string) + ".xml"
				data.Msgs = append(data.Msgs, template.HTML("Found XML from "+fi.ModTime().Local().Format(time.ANSIC)+", configuration URL: <a href='"+xmlurl+"' target='_blank'>"+xmlurl+"</a>"))
			}
			qrcodename := dname + "/" + rows[0]["account"].(string) + "-cfg.png"
			if fi, err := os.Stat(qrcodename); err == nil {
				qrcodeurl := proto + "://" + dnsname + ":" + webport + pathget + "/" + struuid + "/" + rows[0]["account"].(string) + "-cfg.png"
				data.Msgs = append(data.Msgs, template.HTML("Found QR Code from "+fi.ModTime().Local().Format(time.ANSIC)+", URL: <a href='"+qrcodeurl+"' target='_blank'>"+qrcodeurl+"</a><br><img src='"+qrcodeurl+"' />"))
			}
		}
	}
	// return form name to process
	return
}

/*
 * generate the config files needed by the client
 */
func gen_config(rows []map[string]any, data *dat, uuid string, linphone string) (reterr error) {
	// if panic,print/log details
	defer func() {
		if err := recover(); err != nil {
			strerr := HandleErrorWithLines(err.(error))
			reterr = errors.New(strerr)
		}
	}()
	// gather basic info
	proto := os.Getenv("PROTOCOL")
	dnsname := os.Getenv("DNSNAME")
	webport := os.Getenv("WEBPORT")
	localmedia := os.Getenv("LOCALMEDIA")
	pathget := os.Getenv("PATHGET")
	pathfile := os.Getenv("PATHFILE")
	urlfile := proto + "://" + dnsname + ":" + webport + pathfile
	dname := localmedia + "/" + uuid
	vcardname := dname + "/contacts.vcard"
	vcardurl := proto + "://" + dnsname + ":" + webport + pathget + "/" + uuid + "/contacts.vcard"
	xmlname := dname + "/" + rows[0]["account"].(string) + ".xml"
	xmlurl := proto + "://" + dnsname + ":" + webport + pathget + "/" + uuid + "/" + rows[0]["account"].(string) + ".xml"
	qrcodename := dname + "/" + rows[0]["account"].(string) + "-cfg.png"
	qrcodeurl := proto + "://" + dnsname + ":" + webport + pathget + "/" + uuid + "/" + rows[0]["account"].(string) + "-cfg.png"
	// need unique key to link some sections
	// newref := rand.Text()
	newref := RandStringBytesMaskImprSrcUnsafe(20, false)
	// create dir for config files
	err := os.MkdirAll(dname, 0777)
	if err != nil {
		reterr = err
		return
	}
	// query for the vcards to import
	vcardrows, err := query2map("SELECT account, callerid, ext, internal_cnam, description, uuid FROM subacct WHERE ext != '' ORDER BY account;")
	if err != nil {
		reterr = err
		return
	}
	// add details to rows for vcars
	for _, row := range vcardrows {
		// all contacts use same domain
		row["domain"] = rows[0]["domain"]
		// vcard fn will be description if avail, next internal cname if avail, if not... just the extension
		if row["description"] == any("") {
			if row["internal_cnam"] == any("") {
				row["fn"] = row["ext"]
			} else {
				row["fn"] = row["internal_cnam"]
			}
		} else {
			row["fn"] = row["description"]
		}
	}
	// open the vcard file and write the template results
	file, err := os.OpenFile(vcardname, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		reterr = err
		return
	}
	defer file.Close()
	err = tmpl["vcard"].Execute(file, vcardrows)
	if err != nil {
		reterr = err
		return
	}
	err = file.Close()
	if err != nil {
		reterr = err
		return
	}
	// vcard done
	data.Msgs = append(data.Msgs, template.HTML("Generated vcard contacts list for including in XML config: <a href='"+vcardurl+"'>contacts.vcard</a>"))
	// get config default provisioning XML
	resp, err := http.Get("https://subscribe.linphone.org/provisioning")
	if err != nil {
		reterr = errors.New("Linphone provisioning template failed: " + err.Error())
		return
	}
	// read in the entire response
	body, err := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	// structure for original config
	type Entry struct {
		Name      string `xml:"name,attr"`
		Overwrite bool   `xml:"overwrite,attr"`
		CharData  string `xml:",chardata"`
	}
	type Section struct {
		Name  string  `xml:"name,attr"`
		Entry []Entry `xml:"entry"`
	}
	type config struct {
		Xmlns   string    `xml:"xmlns,attr"`
		Section []Section `xml:"section"`
	}
	var orgconfig config
	// xml to structs
	err = xml.Unmarshal(body, &orgconfig)
	if err != nil {
		reterr = err
		return
	}
	resp.Body.Close()
	// new xml config for client
	var newconfig config
	// keep namespace
	newconfig.Xmlns = orgconfig.Xmlns
	// maps by name of indexes into xml
	msec := map[string]int{}
	ment := map[string]int{}
	// handy add tool to XML to newconfig... tracks indexes to names
	add2xml := func(secname string, ent Entry) {
		// already have this section in new? @ index i
		i, ok := msec[secname]
		if !ok {
			// no, so add it to new cfg empty (w/o entries)
			newconfig.Section = append(newconfig.Section, Section{secname, []Entry{}})
			i = len(newconfig.Section) - 1
			msec[secname] = i
		}
		// already have this entry in new? @ index j
		j, ok := ment[secname+"/"+ent.Name]
		if !ok {
			// no, so add it
			newconfig.Section[i].Entry = append(newconfig.Section[i].Entry, ent)
			j = len(newconfig.Section[i].Entry) - 1
			ment[secname+"/"+ent.Name] = j
		} else {
			// yes, so just update the values
			newconfig.Section[i].Entry[j].CharData = ent.CharData
			newconfig.Section[i].Entry[j].Overwrite = ent.Overwrite
		}
	}
	// entries to remove
	delent := []string{"quality_reporting_collector", "conference_factory_uri", "audio_video_conference_factory_uri", "server_addresses",
		"lime_server_url", "rls_uri", "default_lime_x3dh_server_url", "x3dh_server_url", "log_collection_upload_server_url", "config-uri",
		"quality_reporting_interval"}
	// add entries from original XML, dedup and remove unwanted
	for _, osec := range orgconfig.Section {
		for _, oent := range osec.Entry {
			if !slices.Contains(delent, oent.Name) {
				add2xml(osec.Name, oent)
			}
		}
	}
	// diag check
	if ml.gsysLogPri == syslog.LOG_DEBUG {
		for i, sec := range newconfig.Section {
			ml.mylog(syslog.LOG_DEBUG, "Section: "+sec.Name+" index match: "+strconv.FormatBool(i == msec[sec.Name]))
			if i != msec[sec.Name] {
				ml.mylog(syslog.LOG_ERR, "Section: "+sec.Name+" index MISMATCH!")
			}
			for j, ent := range sec.Entry {
				ml.mylog(syslog.LOG_DEBUG, "Entry: "+ent.Name+" index match: "+strconv.FormatBool(j == ment[sec.Name+"/"+ent.Name]))
				if j != ment[sec.Name+"/"+ent.Name] {
					ml.mylog(syslog.LOG_ERR, "Entry: "+ent.Name+" index MISMATCH!")
				}
			}
		}
	}
	// some common changes to the config...
	// misc/config-uri for loading XML at each start... including in XML causes issues w/ sip/default_proxy
	for secname, ents := range map[string][]Entry{"nat_policy_0": {{"ref", true, newref}, {"stun_server", true, "stun.linphone.org"}},
		"misc": {{"contacts-vcard-list", true, vcardurl}, {"hide_chat_rooms_from_removed_proxies", true, "0"}, {"file_transfer_server_url", true, urlfile}, {"log_collection_upload_server_url", true, urlfile}},
		"sip":  {{"media_encryption", true, "srtp"}, {"media_encryption_mandatory", true, "1"}, {"im_notif_policy", true, "none"}, {"default_proxy", true, "0"}, {"use_ipv6", true, "0"}, {"publish_presence", true, "0"}}} {
		for _, ent := range ents {
			add2xml(secname, ent)
		}
	}
	// slice of cfg accts just for msgs note
	cfgaccts := []string{}
	// loop accts to config, adding/updating an auth_info_* and proxy_* section for each
	for i, row := range rows {
		si := strconv.Itoa(i)
		// handy values
		account := row["account"].(string)
		cfgaccts = append(cfgaccts, account)
		domain := row["domain"].(string)
		pwenc := row["pwenc"].(string)
		pw := row["password"].(string)
		regexp := row["max_expiry"].(string)
		// calc ha1...  maybe needed
		ha1text := account + ":" + domain + ":" + pw
		hasher := md5.New()
		hasher.Write([]byte(ha1text))
		ha1 := hex.EncodeToString(hasher.Sum(nil))
		// proxy_*/reg_identity - little different for linphone.org vs voip.ms
		reg_identity := `"` + account + `" <sips:` + account + `@` + domain + `>`
		// setup authentication for acct
		var authinfo map[string][]Entry
		// "pwenc" = "Encrypted" "Clear" "None"
		if domain == "sip.linphone.org" {
			// for linphone acct auth
			reg_identity = `"` + account + `" <sip:` + account + `@` + domain + `;transport=tls>`
			authinfo = map[string][]Entry{"auth_info_" + si: {{"username", true, account}, {"domain", true, domain},
				{"realm", true, domain}, {"ha1", true, ha1}, {"algorithm", true, "MD5"}}}
		} else if pwenc == "Encrypted" {
			// for voip.ms acct w/ ha1 enc pw (realm must match server/domain, usually does...)
			authinfo = map[string][]Entry{"auth_info_" + si: {{"username", true, account}, {"domain", true, domain},
				{"realm", true, domain}, {"ha1", true, ha1}, {"algorithm", true, "MD5"}, {"domain", true, domain}}}
		} else if pwenc == "Clear" {
			// clear text password
			authinfo = map[string][]Entry{"auth_info_" + si: {{"username", true, account}, {"passwd", true, pw}, {"domain", true, domain}}}
		} else {
			// no password, client will prompt
			authinfo = map[string][]Entry{"auth_info_" + si: {{"username", true, account}, {"domain", true, domain}}}
		}
		// config auth_info_* for this acct
		for secname, ents := range authinfo {
			for _, ent := range ents {
				add2xml(secname, ent)
			}
		}
		// only enable push notification if there was a linphone acct
		pn := "1"
		if linphone == "" {
			pn = "0"
		}
		// config proxy_* for this acct
		for secname, ents := range map[string][]Entry{"proxy_" + si: {{"reg_identity", true, reg_identity}, {"quality_reporting_enabled", true, "0"},
			{"publish", true, "0"}, {"reg_proxy", true, "<sip:" + dnsname + ";transport=tls>"}, {"reg_route", true, "<sip:" + dnsname + ";transport=tls>"},
			{"nat_policy_ref", true, newref}, {"avpf", true, "1"}, {"dial_escape_plus", true, "0"}, {"reg_expires", true, regexp}, {"reg_sendregister", true, "1"},
			{"refkey", true, "push_notification"}, {"push_notification", true, pn}, {"remote_push_notification_allowed", true, pn},
			{"push_notification_allowed", true, pn}, {"realm", true, domain}}} {
			for _, ent := range ents {
				add2xml(secname, ent)
			}
		}
	}
	// write the XML config file
	v, err := xml.Marshal(newconfig)
	if err != nil {
		reterr = err
		return
	}
	fi, err := os.OpenFile(xmlname, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		reterr = err
		return
	}
	defer fi.Close()
	_, err = fi.Write(v)
	if err != nil {
		reterr = err
		return
	}
	err = fi.Close()
	if err != nil {
		reterr = err
		return
	}
	// share the news!
	data.Msgs = append(data.Msgs, template.HTML("Generated configuration XML for these accounts: "+strings.Join(cfgaccts, ", ")))
	data.Msgs = append(data.Msgs, template.HTML("XML configuration URL: <a href='"+xmlurl+"' target='_blank'>"+xmlurl+"</a>"))
	// make a QR code for the XML config
	err = qrcode.WriteFile(xmlurl, qrcode.Medium, 256, qrcodename)
	if err != nil {
		reterr = err
		return
	}
	// share it's URL and display it
	data.Msgs = append(data.Msgs, template.HTML("Generated QR Code Config URL: <a href='"+qrcodeurl+"' target='_blank'>"+qrcodeurl+"</a><br><img src='"+qrcodeurl+"' />"))
	// done!
	return
}

/*
 * get details for a panic - called from defered inline function
 */
func HandleErrorWithLines(err error) (m string) {
	if err != nil {
		// look for this
		sourcename := "/mmsgate2.go"
		// other inits
		var pc uintptr
		var filename string
		var line int
		var panic bool
		// look in stack for who called panic
		for i := 1; i < 10; i += 1 {
			pc, filename, line, _ = runtime.Caller(i)
			// first, find panic
			if strings.Contains(filename, "/panic.go") {
				panic = true
			}
			// once found panic...
			if panic {
				// find this module
				if strings.Contains(filename, sourcename) {
					break
				}
			}
		}
		// collect lines from source code
		lines := ""
		// assume source code from runtime okay
		source := filename
		_, err2 := os.Stat(source)
		if err2 != nil {
			// if not, maybe source is same dir as exec
			source = filepath.Dir(os.Args[0]) + sourcename
			_, err2 = os.Stat(source)
		}
		// found source code?
		if err2 == nil {
			// open source code
			file, err2 := os.Open(source)
			if err2 == nil {
				defer file.Close()
				// scan to near error line
				scanner := bufio.NewScanner(file)
				currentLine := 0
				for scanner.Scan() {
					currentLine++
					// near the error line?
					if currentLine > line-3 && currentLine < line+3 {
						// grab lines for log message
						if currentLine == line {
							lines += "\n" + strconv.Itoa(currentLine) + ":>" + scanner.Text()
						} else {
							lines += "\n" + strconv.Itoa(currentLine) + ": " + scanner.Text()
						}
					}
				}
				file.Close()
			}
		}
		// log panic error
		ml.mylog(syslog.LOG_ERR, fmt.Sprintf("[error] in %s[%s:%d] %v%s", runtime.FuncForPC(pc).Name(), filename, line, err, lines))
		// return error
		m = err.Error()
	}
	return
}

/*
 * submit url to Linphone.org and return result.
 */
func linphone_api(url string, method string, data map[string]string, digtan *digest.Transport, apikey string) (body []byte, err error) {
	// client for connections
	var client http.Client
	// need to force to IPv4
	var zeroDialer net.Dialer
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return zeroDialer.DialContext(ctx, "tcp4", addr)
	}
	// passed pointer to digest auth transport?  if so, use it
	if digtan == nil {
		client = http.Client{}
		client.Transport = transport
	} else {
		digtan.Transport = transport
		client = http.Client{Transport: digtan}
	}
	// translate map to json data to post
	var reqbody io.Reader
	if len(data) > 0 {
		reqdata, err := json.Marshal(data)
		if err == nil {
			reqbody = bytes.NewReader(reqdata)
		}
	}
	// get ready to send request
	req, err := http.NewRequest(method, url, reqbody)
	if err != nil {
		ml.mylog(syslog.LOG_ERR, "Linphone's API call failed [NewRequest]: "+err.Error())
		err = errors.New("Linphone's API call failed [NewRequest]: " + err.Error())
		return
	}
	// default headers needed for linphone
	req.Header = http.Header{
		"Content-Type": {"application/json"},
		"accept":       {"application/json"}}
	// one more header for auth
	if digtan != nil {
		// needed for digest auto
		req.Header.Add("from", "sip:"+digtan.Username+"@sip.linphone.org")
	} else if apikey != "" {
		// need for apikey auth
		req.Header.Add("x-api-key", apikey)
	}
	// send request
	resp, err := client.Do(req)
	if err != nil {
		ml.mylog(syslog.LOG_ERR, "Linphone's API call failed [client.Do]: "+err.Error())
		err = errors.New("Linphone's API call failed [client.Do]: " + err.Error())
		return
	}
	// read in the entire response
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		ml.mylog(syslog.LOG_ERR, "Linphone's API call failed [io.ReadAll]: "+err.Error())
		err = errors.New("Linphone's API call failed [io.ReadAll]: " + err.Error())
	}
	defer resp.Body.Close()
	// check http status
	if resp.StatusCode > 299 {
		ml.mylog(syslog.LOG_ERR, "Linphone's API call failed with status ["+strconv.Itoa(resp.StatusCode)+"]")
		err = errors.New("Linphone's API call failed with status [" + strconv.Itoa(resp.StatusCode) + "]")
	}
	ml.mylog(syslog.LOG_DEBUG, "Linphone's API call response code and body: ("+strconv.Itoa(resp.StatusCode)+") "+string(body))
	resp.Body.Close()
	return
}

// used for random password and ref keys
const (
	letterBytes   = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ_!#-=" // 57 possibilities
	letterIdxBits = 6                                                           // 6 bits to represent 64 possibilities / indexes
	letterIdxMask = 1<<letterIdxBits - 1                                        // All 1-bits, as many as letterIdxBits
	letterIdxMax  = 63 / letterIdxBits                                          // # of letter indices fitting in 63 bits
)

// seed the random gen
var src = rand.NewSource(time.Now().UnixNano())

/*
 * returns n random chars from letterBytes. maybe include specal chars
 */
func RandStringBytesMaskImprSrcUnsafe(n int, special bool) string {
	// ignore last 5 of letterBytes, special characters
	skip := 5
	// if requested, include them
	if special {
		skip = 0
	}
	b := make([]byte, n)
	// A src.Int63() generates 63 random bits, enough for letterIdxMax characters!
	for i, cache, remain := n-1, src.Int63(), letterIdxMax; i >= 0; {
		if remain == 0 {
			cache, remain = src.Int63(), letterIdxMax
		}
		if idx := int(cache & letterIdxMask); idx < len(letterBytes)-skip {
			b[i] = letterBytes[idx]
			i--
		}
		cache >>= letterIdxBits
		remain--
	}
	return *(*string)(unsafe.Pointer(&b))
}

// handy tool to send message to log and web page
func dualMsg(msgs *[]template.HTML, p syslog.Priority, m string) {
	*msgs = append(*msgs, template.HTML(m))
	ml.mylog(p, m)
}

/*
 * generate the Linphone acct form and perform add/updates/deletes
 */
func linmenu(Form url.Values, data *dat) (retfrorm string, reterr error) {
	retfrorm = "form-table-2d"
	// if panic,print/log details
	defer func() {
		if err := recover(); err != nil {
			strerr := HandleErrorWithLines(err.(error))
			reterr = errors.New(strerr)
		}
	}()
	// handy tool to send message to log and web page
	//dualMsg := func(p syslog.Priority, m string) {
	//	data.Msgs = append(data.Msgs, template.HTML(m))
	//	ml.mylog(p, m)
	//}
	// some initial form values
	data.CustomItems = []any{}
	data.Btn1 = true
	data.Btn2 = true
	data.Thispage = "linmenu"
	data.Nextpage = "linmenu"
	data.Title2 = " - Manage Linphone Accounts"
	lynx := strings.Contains(Form.Get("useragent"), "Lynx")
	// first, process any submitted form requests
	for i := 0; ; i += 1 {
		si := strconv.Itoa(i)
		// get username from form
		username := Form.Get("username-" + si)
		// none, we are done
		if username == "" {
			break
		}
		// request to delete?
		if Form.Get("delete-"+si) == "Delete" {
			ml.mylog(syslog.LOG_DEBUG, "Delete linphone acct "+username)
			// delete it
			_, err := db.Exec("DELETE FROM linphone WHERE username=?", username)
			if err != nil {
				dualMsg(&data.Msgs, syslog.LOG_ERR, "Error deleting Linphone acct: "+err.Error())
			} else {
				data.Msgs = append(data.Msgs, template.HTML("Deleted Linphone acct: "+username))
			}
			// done delete acct
			break
		}
		// request to add?
		if Form.Get("action-"+si) == "Add" {
			// bad name?
			if username == "" || strings.ContainsFunc(username, func(r rune) bool {
				return !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("!#$%&'*+-/=?^_`{|}~", r))
			}) {
				// bad name!
				data.Msgs = append(data.Msgs, template.HTML("Invalid Linphone acct name: "+username))
			} else {
				// good name...  is it already an existing acct?
				body, err := linphone_api("https://subscribe.linphone.org/api/accounts/"+username+"@sip.linphone.org/info", "GET", map[string]string{}, nil, "")
				// 404 not found means acct not found
				if err != nil && !strings.Contains(err.Error(), "[404]") {
					dualMsg(&data.Msgs, syslog.LOG_ERR, "Linphone request failed on info for "+username+": "+err.Error())
				} else {
					// values if active or not but need password
					var captcha_url sql.NullString
					var account_creation_request_token sql.NullString
					var password sql.NullString
					// must have been the 404, acct not found
					if err != nil {
						// it was NOT found... create new acct!
						body, err = linphone_api("https://subscribe.linphone.org/api/account_creation_request_tokens", "POST", map[string]string{}, nil, "")
						if err != nil {
							dualMsg(&data.Msgs, syslog.LOG_ERR, "Linphone request failed on account_creation_request_tokens: "+err.Error())
						} else {
							respjsontok := struct {
								Token          string
								Validation_url string
							}{}
							err = json.Unmarshal(body, &respjsontok)
							if err != nil {
								dualMsg(&data.Msgs, syslog.LOG_ERR, "Linphone Unmarshal failed on account_creation_request_tokens: "+err.Error())
							} else {
								// got values needed for creating new account
								password.String = RandStringBytesMaskImprSrcUnsafe(20, true)
								password.Valid = true
								captcha_url.String = respjsontok.Validation_url
								captcha_url.Valid = true
								account_creation_request_token.String = respjsontok.Token
								account_creation_request_token.Valid = true
							}
						}
					}
					// it was found, maybe active, or token created for new acct
					_, err = db.Exec("INSERT INTO linphone(username,password,account_creation_request_token,captcha_url) VALUES(?,?,?,?);",
						username, password, account_creation_request_token, captcha_url)
					if err != nil {
						dualMsg(&data.Msgs, syslog.LOG_ERR, "Linphone SQL insert failed for "+username+": "+err.Error())
					} else {
						if !password.Valid {
							data.Msgs = append(data.Msgs, template.HTML("Added Linphone "+username+" as existing account.  Please enter password."))
						} else {
							data.Msgs = append(data.Msgs, template.HTML("Added Linphone "+username+" as new account.  Please solve <a href='"+captcha_url.String+"' target='_blank'>CAPTCHA</a>."))
						}
					}
				}
			}
			// done add acct
			break
		}
		// solve the captcha
		if Form.Get("action-"+si) == "Solved" {
			// get account details
			query := "SELECT password,account_creation_request_token FROM linphone WHERE username = ?;"
			rows, err := query2map(query, username)
			if err != nil {
				dualMsg(&data.Msgs, syslog.LOG_ERR, "Getting Linphone acct for Solved: "+err.Error())
			}
			// there should be 1 row for a loop
			for _, row := range rows {
				// need these for request to create account
				password := row["password"].(string)
				account_creation_request_token := row["account_creation_request_token"].(string)
				// request to create account w/ token
				body, err := linphone_api("https://subscribe.linphone.org/api/account_creation_tokens/using-account-creation-request-token", "POST",
					map[string]string{"account_creation_request_token": account_creation_request_token}, nil, "")
				if err != nil {
					dualMsg(&data.Msgs, syslog.LOG_ERR, "Linphone request failed on account_creation_tokens: "+err.Error())
				} else {
					// should have returned an acct create token
					respjson := struct {
						Token string
					}{}
					err = json.Unmarshal(body, &respjson)
					if err != nil {
						dualMsg(&data.Msgs, syslog.LOG_ERR, "Linphone Unmarshal failed on account_creation_tokens for "+username+": "+err.Error())
					} else {
						// finally, with acct create token, we can create an inactive acct
						body, err := linphone_api("https://subscribe.linphone.org/api/accounts/with-account-creation-token", "POST",
							map[string]string{"username": username, "password": password, "algorithm": "MD5", "account_creation_token": respjson.Token}, nil, "")
						if err != nil {
							dualMsg(&data.Msgs, syslog.LOG_ERR, "Linphone request failed on with-account-creation-token: "+err.Error())
						} else {
							// should have provided domain for new acct
							respjsonacct := struct {
								Domain string
							}{}
							err = json.Unmarshal(body, &respjsonacct)
							if err != nil {
								dualMsg(&data.Msgs, syslog.LOG_ERR, "Linphone Unmarshal failed on with-account-creation-token for "+username+": "+err.Error())
							} else {
								// save it in db
								_, err = db.Exec("UPDATE linphone SET account_creation_request_token = NULL, captcha_url = NULL, domain = ?, account_creation_token = ? WHERE username = ?;",
									respjsonacct.Domain, respjson.Token, username)
								if err != nil {
									dualMsg(&data.Msgs, syslog.LOG_ERR, "Linphone SQL update failed for "+username+": "+err.Error())
								} else {
									ml.mylog(syslog.LOG_DEBUG, "Solved successful.  Inactive account "+username+" needs activation. Please enter email address to activate.")
								}
							}
						}
					}
				}
			}
			// done solve CAPTCHA
			break
		}
		// set password for existing account just added
		if Form.Get("action-"+si) == "Set Password" {
			// try password for the existing acct
			password := Form.Get("entry-" + si)
			body, err := linphone_api("https://subscribe.linphone.org/api/accounts/me", "GET",
				nil, &digest.Transport{
					Username: username,
					Password: password}, "")
			if err != nil {
				dualMsg(&data.Msgs, syslog.LOG_ERR, "Linphone request failed on password attempt: "+err.Error())
			} else {
				// worked... should have returned an acct create token
				respjson := struct {
					Username           string
					Domain             string
					Email              string
					Activated          bool
					Provisioning_token string
				}{}
				err = json.Unmarshal(body, &respjson)
				if err != nil {
					dualMsg(&data.Msgs, syslog.LOG_ERR, "Linphone Unmarshal failed on password attempt for "+username+": "+err.Error())
				} else {
					// get values to put in db
					email := sql.NullString{String: respjson.Email, Valid: true}
					account_creation_token := sql.NullString{String: "dummy", Valid: true}
					apikey := ""
					if respjson.Email == "" {
						email.Valid = false
					}
					if respjson.Activated {
						apikey, err = check_apikey(username, password, "")
						if err != nil {
							dualMsg(&data.Msgs, syslog.LOG_ERR, "Linphone ckeck_api failed for "+username+": "+err.Error())
						}
					}
					// put it in the db
					_, err = db.Exec("UPDATE linphone SET password = ?, domain = ?, email = ?, activated = ?, provisioning_token = ?, account_creation_token = ?, apikey = ? WHERE username = ?;",
						Form.Get("entry-"+si), respjson.Domain, email, respjson.Activated, respjson.Provisioning_token, account_creation_token, apikey, username)
					if err != nil {
						dualMsg(&data.Msgs, syslog.LOG_ERR, "Linphone SQL password update failed for "+username+": "+err.Error())
					} else {
						data.Msgs = append(data.Msgs, template.HTML("Updated Linphone password for "+username+"."))
					}
				}
			}
			// done set password
			break
		}
		// entered an email address
		if Form.Get("action-"+si) == "Set Email" {
			// get account details
			query := "SELECT password FROM linphone WHERE username = ?;"
			rows, err := query2map(query, username)
			if err != nil {
				dualMsg(&data.Msgs, syslog.LOG_ERR, "SQL error getting Linphone acct info for Set Email: "+err.Error())
			}
			// there should be 1 row for a loop
			for _, row := range rows {
				// need these for request to set email
				password := row["password"].(string)
				email := Form.Get("entry-" + si)
				// set the email
				body, err := linphone_api("https://subscribe.linphone.org/api/accounts/me/email/request", "POST",
					map[string]string{"email": email}, &digest.Transport{
						Username: username,
						Password: password}, "")
				if err != nil {
					dualMsg(&data.Msgs, syslog.LOG_ERR, "Linphone request failed on set email: "+err.Error()+string(body))
				} else {
					// worked, put it in the db
					_, err = db.Exec("UPDATE linphone SET account_creation_token = NULL, email = ? WHERE username = ?;",
						email, username)
					if err != nil {
						dualMsg(&data.Msgs, syslog.LOG_ERR, "Linphone SQL email update failed for "+username+": "+err.Error())
					} else {
						data.Msgs = append(data.Msgs, template.HTML("Updated Linphone email for "+username+" to "+email+"."))
					}
				}
			}
			// done w/ email update
			break
		}
		// entered code to verify email (and activate acct)
		if Form.Get("action-"+si) == "Enter code" {
			// get account details
			query := "SELECT password FROM linphone WHERE username = ?;"
			rows, err := query2map(query, username)
			if err != nil {
				dualMsg(&data.Msgs, syslog.LOG_ERR, "Getting Linphone acct info for Enter code: "+err.Error())
			}
			// there should be 1 row for a loop
			for _, row := range rows {
				// need these for request to set email
				password := row["password"].(string)
				code := Form.Get("entry-" + si)
				// set the email
				body, err := linphone_api("https://subscribe.linphone.org/api/accounts/me/email", "POST",
					map[string]string{"code": code}, &digest.Transport{
						Username: username,
						Password: password}, "")
				if err != nil {
					dualMsg(&data.Msgs, syslog.LOG_ERR, "Linphone request failed on Enter code: "+err.Error()+string(body))
				}
				// should have returned an acct details
				respjson := struct {
					Username           string
					Domain             string
					Email              string
					Activated          bool
					Provisioning_token string
				}{}
				err2 := json.Unmarshal(body, &respjson)
				if err2 != nil {
					dualMsg(&data.Msgs, syslog.LOG_ERR, "Linphone Unmarshal failed on account_creation_tokens for "+username+": "+err2.Error())
				}
				// issue w/ api call, unmarshal, or name mismatches?
				if err != nil || err2 != nil || respjson.Username != username {
					// clear the email and back to needing to enter eail
					_, err = db.Exec("UPDATE linphone SET email = NULL, account_creation_token = 'dummy' WHERE username = ?;", username)
					if err != nil {
						dualMsg(&data.Msgs, syslog.LOG_ERR, "Linphone SQL failed for email verify for "+username+": "+err.Error())
					} else {
						dualMsg(&data.Msgs, syslog.LOG_DEBUG, "Linphone verify email failed.  The email address was removed for "+username+".")
					}
				} else {
					apikey := ""
					if respjson.Activated {
						apikey, err = check_apikey(username, password, "")
						if err != nil {
							dualMsg(&data.Msgs, syslog.LOG_ERR, "Linphone check_api failed for "+username+": "+err.Error())
						}
					}
					// all worked, so should be active!
					_, err = db.Exec("UPDATE linphone SET activated = ?, provisioning_token = ?, apikey = ? WHERE username = ?;",
						respjson.Activated, respjson.Provisioning_token, apikey, username)
					if err != nil {
						dualMsg(&data.Msgs, syslog.LOG_ERR, "Linphone SQL failed for email verify for "+username+": "+err.Error())
					} else {
						dualMsg(&data.Msgs, syslog.LOG_DEBUG, "Linphone email verification was successful for "+username+".")
					}
				}
			}
			// done w/ email verify
			break
		}
	}
	// now finally display current data
	query := "SELECT username, CASE WHEN activated=1 THEN 'Activated!' WHEN password IS NULL THEN 'Enter password' WHEN account_creation_request_token IS NOT NULL THEN captcha_url " +
		"WHEN account_creation_token IS NOT NULL THEN 'Add email' WHEN email IS NOT NULL THEN 'Verify email' ELSE '' END AS status FROM linphone ORDER BY username;"
	rows, err := query2map(query)
	if err != nil {
		ml.mylog(syslog.LOG_ERR, "Getting Linphone accts for linmenu: "+err.Error())
		data.Msgs = append(data.Msgs, template.HTML("Error getting Linphone accts: "+err.Error()))
	}
	// header row
	data.CustomItems = append(data.CustomItems, struct {
		Header bool
		Row    []any
	}{true, []any{"Linacct", "", "", "Status", "Entry", "", ""}})
	// flag for verify email note/msg
	var emailverifymsg bool
	// rows for accts from linphone db table
	for i, row := range rows {
		// tag to get the acct name later when apply hit
		accounthiddentag := template.HTML(fmt.Sprintf("<input id='username-%d' name='username-%d' type='hidden' value='%s'>", i, i, row["username"]))
		// tag for delete button
		deletetag := template.HTML(fmt.Sprintf("<input id='delete-%d' name='delete-%d' type='submit' value='Delete'>", i, i))
		// usually, no entry or action needed
		action := template.HTML("")
		entry := template.HTML("")
		// status normally just the db column
		status := template.HTML(row["status"].(string))
		// if status is the captcha url, make it a link (unless lynx browser) and display solved button
		if strings.HasPrefix(string(status), "http") {
			if !lynx {
				status = template.HTML("<a href='" + status + "' target='_blank'>Solve CAPTCHA</a>")
			} else {
				status = template.HTML("Solve: " + status)
			}
			action = template.HTML(fmt.Sprintf("<input id='action-%d' name='action-%d' type='submit' value='Solved'>", i, i))
			// need password?  display prompt and button
		} else if status == "Enter password" {
			entry = template.HTML(fmt.Sprintf("<input id='entry-%d' name='entry-%d' type='password' value=''>", i, i))
			action = template.HTML(fmt.Sprintf("<input id='action-%d' name='action-%d' type='submit' value='Set Password'>", i, i))
			// need email address?  display prompt and button
		} else if status == "Add email" {
			entry = template.HTML(fmt.Sprintf("<input id='entry-%d' name='entry-%d' type='text' value=''>", i, i))
			action = template.HTML(fmt.Sprintf("<input id='action-%d' name='action-%d' type='submit' value='Set Email'>", i, i))
			// need email verify code?  display prompt and button
		} else if status == "Verify email" {
			entry = template.HTML(fmt.Sprintf("<input id='entry-%d' name='entry-%d' type='text' value=''>", i, i))
			action = template.HTML(fmt.Sprintf("<input id='action-%d' name='action-%d' type='submit' value='Enter code'>", i, i))
			emailverifymsg = true
		}
		// display the row
		data.CustomItems = append(data.CustomItems, struct {
			Header bool
			Row    []any
		}{false, []any{row["username"], accounthiddentag, "", status, entry, action, deletetag}})
	}
	// extra row for new acct for linphone db table
	i := len(rows)
	accountaddtag := template.HTML(fmt.Sprintf("<input id='username-%d' name='username-%d' type='text' value=''>", i, i))
	addtag := template.HTML(fmt.Sprintf("<input id='action-%d' name='action-%d' type='submit' value='Add'>", i, i))
	data.CustomItems = append(data.CustomItems, struct {
		Header bool
		Row    []any
	}{false, []any{accountaddtag, "", "", "", "", addtag}})
	// just a helpful note
	if emailverifymsg {
		data.Msgs = append(data.Msgs, template.HTML("Email verification codes last 10 minutes.  If you don't receive the code, entering a random invalid code will remove the email and you can try again."))
	}
	return
}

/*
 * dump the sql db tables
 */
func sqldump(Form url.Values, data *dat) (retfrorm string, reterr error) {
	// form for default menu
	retfrorm = "form-table-menu"
	// the sqllite3 cli already has html table output, we'll use that
	cmdout, err := exec.Command("bash", "-c", ". /etc/opensips/globalcfg.sh; sqlite3 -html -header $DBPATHM \""+
		"SELECT rowid,msgid,strftime('%Y-%m-%d %H:%M',datetime(rcvd_ts, 'unixepoch', 'localtime')) as rcvd_ts, "+
		"strftime('%Y-%m-%d %H:%M',datetime(sent_ts, 'unixepoch', 'localtime')) as sent_ts,fromid,fromdom,toid,todom, "+
		"substr(message,1,30) as message,direction as dir,msgstatus as msgstat,did,msgtype,trycnt FROM send_msgs order by rowid;\"").Output()
	if err != nil {
		data.Msgs = append(data.Msgs, template.HTML("SQL dump of table \"send_msgs\" returned error: "+err.Error()))
	} else {
		data.Msgs = append(data.Msgs, template.HTML("Database dump of table \"send_msgs\":<br><table>"+string(cmdout)+"</table>"))
	}
	// also subacct table
	cmdout, err = exec.Command("bash", "-c", ". /etc/opensips/globalcfg.sh; sqlite3 -html -header $DBPATHM \""+
		"SELECT rowid,* FROM subacct order by rowid;\"").Output()
	if err != nil {
		data.Msgs = append(data.Msgs, template.HTML("SQL dump of table \"subacct\" returned error: "+err.Error()))
	} else {
		data.Msgs = append(data.Msgs, template.HTML("Database dump of table \"subacct\":<br><table>"+string(cmdout)+"</table>"))
	}
	// and linphone table
	cmdout, err = exec.Command("bash", "-c", ". /etc/opensips/globalcfg.sh; sqlite3 -html -header $DBPATHM \""+
		"SELECT rowid,* FROM linphone order by rowid;\"").Output()
	if err != nil {
		data.Msgs = append(data.Msgs, template.HTML("SQL dump of table \"linphone\" returned error: "+err.Error()))
	} else {
		data.Msgs = append(data.Msgs, template.HTML("Database dump of table \"linphone\":<br><table>"+string(cmdout)+"</table>"))
	}
	// finally opensips silo table.  it's a msg queue
	cmdout, err = exec.Command("bash", "-c", ". /etc/opensips/globalcfg.sh; sqlite3 -html -header $DBPATH \""+
		"SELECT id, src_addr, dst_addr, username, domain, "+
		"strftime('%Y-%m-%d %H:%M',datetime(inc_time, 'unixepoch', 'localtime')) as inc_time, "+
		"strftime('%Y-%m-%d %H:%M',datetime(exp_time, 'unixepoch', 'localtime')) as exp_time, "+
		"strftime('%Y-%m-%d %H:%M',datetime(snd_time, 'unixepoch', 'localtime')) as snd_time, ctype, substr(body,1,30) as body FROM silo order by rowid;\"").Output()
	if err != nil {
		data.Msgs = append(data.Msgs, template.HTML("SQL dump of OpenSIPS table \"silo\" returned error: "+err.Error()))
	} else {
		data.Msgs = append(data.Msgs, template.HTML("Database dump of OpenSIPS table \"silo\":<br><table>"+string(cmdout)+"</table>"))
	}
	return
}

/*
 * checks the Linphone apikey.  returns new key if needed, otherwise same key.
 */
func check_apikey(username string, password string, apikey string) (newapikey string, err error) {
	ml.mylog(syslog.LOG_DEBUG, "Check apikey called for "+username+" key: "+apikey)
	// default return empty string on fatal error
	newapikey = ""
	// flag to get new key
	var neednewkey bool
	// no key to check?
	if apikey == "" {
		neednewkey = true
	} else {
		// try apikey for the existing acct
		body, err2 := linphone_api("https://subscribe.linphone.org/api/accounts/me", "GET",
			nil, nil, apikey)
		if err2 != nil {
			// assume 401 - key expired/bad
			ml.mylog(syslog.LOG_WARNING, "Check apikey failed.  Issuing new key: "+err2.Error())
			neednewkey = true
		} else {
			// worked... should have returned an acct create token
			respjson := struct {
				Username string
			}{}
			// check the result so as to compare username
			err2 = json.Unmarshal(body, &respjson)
			if err2 != nil {
				ml.mylog(syslog.LOG_WARNING, "Check apikey Unmarshal failed.  Issuing new key: "+err2.Error())
				neednewkey = true
			} else {
				if respjson.Username != username {
					neednewkey = true
					ml.mylog(syslog.LOG_WARNING, "Check apikey name mismatch.  Issuing new key.")
				} else {
					ml.mylog(syslog.LOG_DEBUG, "Check apikey successful "+username+" key: "+apikey)
				}
			}
		}
	}
	// need a new apikey?
	if neednewkey {
		// request it
		body, err := linphone_api("https://subscribe.linphone.org/api/accounts/me/api_key", "GET", nil,
			&digest.Transport{
				Username: username,
				Password: password}, "")
		// bad if error
		if err != nil {
			ml.mylog(syslog.LOG_ERR, "Check apikey failed to get new key for "+username+", error: "+err.Error())
			err = errors.New("Check apikey failed to get new key for " + username + ", error: " + err.Error())
		} else {
			// the apikey is just returned, no json
			newapikey = string(body)
			ml.mylog(syslog.LOG_DEBUG, "Check apikey successful "+username+" new requested key: "+newapikey)
		}
	} else {
		// return the verified apikey
		newapikey = apikey
	}
	return
}

/*
 * This checks all the apikays in the db for activated accounts
 */
func apikeychecks() (err error) {
	// place to put data from the db
	var username, password, apikey string
	// only check the activated accounts
	rows, err := query2map("SELECT username,password,apikey FROM linphone WHERE activated=1;")
	if err != nil {
		ml.mylog(syslog.LOG_ERR, "query2map error getting apikeys to check: "+err.Error())
		return errors.New("query2map error getting apikeys to check: " + err.Error())
	}
	// loop each active acct
	for _, row := range rows {
		// get id, pw and apikey
		username, password = row["username"].(string), row["password"].(string)
		if row["apikey"] == nil {
			apikey = ""
		} else {
			apikey = row["apikey"].(string)
		}
		// check the current key
		newapikey, err := check_apikey(username, password, apikey)
		if err != nil {
			ml.mylog(syslog.LOG_ERR, "check_apikey return error, apikeychecks: "+err.Error())
			return errors.New("check_apikey return error, apikeychecks: " + err.Error())
		}
		// did we get a new key?
		if newapikey != apikey {
			// put it in the db
			_, err = db.Exec("UPDATE linphone SET apikey = ? WHERE username =?;", newapikey, username)
			if err != nil {
				ml.mylog(syslog.LOG_ERR, "SQL error updating apikey: "+err.Error())
				return errors.New("SQL error updating apikey: " + err.Error())
			}
		}
	}
	return nil
}

/*
 * This will loop and run apikeychecks every APIKEYHRS hours
 */
func sched_apikeychecks() {
	// need this for how often to reconcile
	apikeyhrs, err := strconv.ParseInt(os.Getenv("APIKEYHRS"), 10, 64)
	// ascii to int fail?
	if err != nil {
		ml.mylog(syslog.LOG_WARNING, "Bad environment variable 'APIKEYHRS'.  Defaulting to 5 hours between API key checks.")
		apikeyhrs = 5
	}
	// too long or short?
	if apikeyhrs > 24 || apikeyhrs < 1 {
		ml.mylog(syslog.LOG_WARNING, "Environment variable 'APIKEYHRS'. 'APIKEYHRS' not 1 - 24.  Defaulting to 5 hours between reconciles.")
		apikeyhrs = 5
	}
	for {
		err = apikeychecks()
		if err != nil {
			ml.mylog(syslog.LOG_ERR, "API key checks failed: "+err.Error())
		}
		// sleep for APIKEYHRS hours
		time.Sleep(time.Duration(apikeyhrs) * time.Hour)
	}
}

/*
 * These two funcs set/get env vars locally and in globalcfg.txt
 */
func get_global(gsetting string) (value string, err error) {
	path := os.Getenv("GLOBALCFG")
	cmdout, err := exec.Command("bash", "-c", ". "+path+"; echo $"+gsetting+";").Output()
	value = strings.Trim(string(cmdout), "\n")
	ml.mylog(syslog.LOG_DEBUG, "get_global "+gsetting+": "+value)
	if err != nil {
		ml.mylog(syslog.LOG_ERR, "get_global "+gsetting+": "+err.Error())
	}
	return
}
func set_global(gsetting string, value string) (err error) {
	path := os.Getenv("GLOBALCFG")
	id := os.Getenv("USERNAME")
	var out []byte
	if id == "mmsgate" {
		out, err = exec.Command("sudo", path, "gup", gsetting, value).Output()
	} else {
		out, err = exec.Command(path, "gup", gsetting, value).Output()
	}
	ml.mylog(syslog.LOG_DEBUG, "set_global "+gsetting+": "+value)
	os.Setenv(gsetting, value)
	v, _ := get_global(gsetting)
	if v != value {
		ml.mylog(syslog.LOG_ERR, "set_global "+gsetting+": "+string(out)+" failed.")
	} else {
		err = nil
	}
	return
}

/*
 * enable opensips
 */
func enableopensips(Form url.Values, data *dat) (retfrorm string, err error) {
	// form for default menu
	retfrorm = "form-table-menu"
	v, err := get_global("ENABLEOPENSIPS")
	if err != nil {
		dualMsg(&data.Msgs, syslog.LOG_ERR, "get_global returned error: "+err.Error())
	}
	if v == "Y" {
		dualMsg(&data.Msgs, syslog.LOG_DEBUG, "OpenSIPS already enabled.")
	} else {
		err = set_global("ENABLEOPENSIPS", "Y")
		if err != nil {
			dualMsg(&data.Msgs, syslog.LOG_ERR, "set_global returned error: "+err.Error())
		}
		dualMsg(&data.Msgs, syslog.LOG_DEBUG, "OpenSIPS is now enabled.")
	}
	return
}

/*
 * disable opensips
 */
func disableopensips(Form url.Values, data *dat) (retfrorm string, reterr error) {
	// form for default menu
	retfrorm = "form-table-menu"
	v, err := get_global("ENABLEOPENSIPS")
	if err != nil {
		dualMsg(&data.Msgs, syslog.LOG_ERR, "get_global returned error: "+err.Error())
	}
	if v == "N" {
		dualMsg(&data.Msgs, syslog.LOG_DEBUG, "OpenSIPS already disabled.")
	} else {
		err = set_global("ENABLEOPENSIPS", "N")
		if err != nil {
			dualMsg(&data.Msgs, syslog.LOG_ERR, "set_global returned error: "+err.Error())
		}
		dualMsg(&data.Msgs, syslog.LOG_DEBUG, "OpenSIPS is now disabled.")
	}
	return
}

/*
 * stop opensips
 */
func stopopensips(Form url.Values, data *dat) (retfrorm string, err error) {
	// form for default menu
	retfrorm = "form-table-menu"
	_, err = exec.Command("sudo", "/scripts/stopopensips.sh").Output()
	ml.mylog(syslog.LOG_DEBUG, "stopopensips")
	if err != nil {
		dualMsg(&data.Msgs, syslog.LOG_ERR, "stopopensips: "+err.Error())
	} else {
		dualMsg(&data.Msgs, syslog.LOG_DEBUG, "OpenSIPS has been stopped.")
	}
	return
}

// needed for log/xlog lvl form
var desc2lvl = map[string]string{"Alert level": "-3",
	"Critical level":         "-2",
	"Error level":            "-1",
	"Warning level":          "1",
	"Notice level (default)": "2",
	"Info level":             "3",
	"Debug level":            "4"}
var lvls = [][]any{{"Alert level"}, {"Critical level"}, {"Error level"}, {"Warning level"}, {"Notice level (default)"}, {"Info level"}, {"Debug level"}}

// to build form to display form-table-custom template
type custom struct {
	ItemDesc string
	ItemHTML template.HTML
}

/*
 * display/modify opensips log level
 */
func setloglevel(Form url.Values, data *dat) (retfrorm string, err error) {
	// form for default menu
	retfrorm = "form-table-custom"
	data.Nextpage = "advmenu-setloglevel"
	data.Thispage = "advmenu-setloglevel"
	data.Title2 = " - Set OpenSIPS Log Level"
	data.Btn2 = true
	data.Btn2val = "Cancel"
	// assume lvl 2 notice if can't get it
	currlvl := "2"
	var currlvldesc string
	// did they hit 'Apply'?
	if Form.Get("log-apply") == "Apply" {
		// get selected lvl desc
		newlvldesc := Form.Get("log-lvl")
		// get the lvl number
		newlvl, ok := desc2lvl[newlvldesc]
		if ok {
			dualMsg(&data.Msgs, syslog.LOG_DEBUG, "Setting OpenSIPS log level to "+newlvldesc)
			// set it to new level
			_, err := exec.Command("opensips-cli", "-x", "mi", "log_level", newlvl).Output()
			// check return code
			if err != nil {
				dualMsg(&data.Msgs, syslog.LOG_ERR, "Failed to set OpenSIPS log level to "+newlvldesc)
			} else {
				dualMsg(&data.Msgs, syslog.LOG_ERR, "Successfully set OpenSIPS log level to "+newlvldesc)
			}
		} else {
			// bad lvl desc from form
			dualMsg(&data.Msgs, syslog.LOG_ERR, "Bad OpenSIPS log level: "+newlvldesc)
		}
	}
	// get current level (maybe just set)
	cmdout, err := exec.Command("opensips-cli", "-x", "mi", "log_level").Output()
	// chk ret code
	if err != nil {
		dualMsg(&data.Msgs, syslog.LOG_WARNING, "Failed get log level via OpenSIPS CLI: "+err.Error())
	} else {
		ml.mylog(syslog.LOG_DEBUG, "OpenSIPS CLI result: "+string(cmdout))
		// parse the status message from the JSON response
		type lglvl struct {
			Processes []struct {
				PID       int
				Log_level int `json:"Log level"`
				Type      string
			}
		}
		var oresp lglvl
		err = json.Unmarshal(cmdout, &oresp)
		// fail to parse?
		if err != nil {
			ml.mylog(syslog.LOG_WARNING, "Failed to parse results from OpenSIPS CLI: "+err.Error())
		} else {
			// store the current log level
			currlvl = strconv.Itoa(oresp.Processes[0].Log_level)
		}
	}
	// check each from map
	for k, v := range desc2lvl {
		// if the current level, store lvl desc
		if v == currlvl {
			currlvldesc = k
		}
	}
	// select tag w/ options pull-down
	selecttag, err := template2html("selecttag", struct {
		Name  string
		Value any
		Rows  [][]any
	}{"log-lvl", currlvldesc, lvls})
	// button to appy new level
	applytag := template.HTML("<input id='log-apply' name='log-apply' type='submit' value='Apply'>")
	// add tags to form
	data.CustomItems = []any{custom{"OpenSIPS Log Level", selecttag},
		custom{"Press 'Apply' to set new level", applytag}}
	return
}

/*
 * display/modify opensips xlog level
 */
func setxloglevel(Form url.Values, data *dat) (retfrorm string, err error) {
	// form for default menu
	retfrorm = "form-table-custom"
	data.Nextpage = "advmenu-setxloglevel"
	data.Thispage = "advmenu-setxloglevel"
	data.Title2 = " - Set OpenSIPS xLog Level"
	data.Btn2 = true
	data.Btn2val = "Cancel"
	// assume lvl 2 notice if can't get it
	currlvl := "2"
	var currlvldesc string
	// did they hit 'Apply'?
	if Form.Get("log-apply") == "Apply" {
		// get selected lvl desc
		newlvldesc := Form.Get("log-lvl")
		// get the lvl number
		newlvl, ok := desc2lvl[newlvldesc]
		if ok {
			dualMsg(&data.Msgs, syslog.LOG_DEBUG, "Setting OpenSIPS xlog level to "+newlvldesc)
			// set it to new level
			_, err := exec.Command("opensips-cli", "-x", "mi", "xlog_level", newlvl).Output()
			// check return code
			if err != nil {
				dualMsg(&data.Msgs, syslog.LOG_ERR, "Failed to set OpenSIPS xlog level to "+newlvldesc)
			} else {
				dualMsg(&data.Msgs, syslog.LOG_ERR, "Successfully set OpenSIPS xlog level to "+newlvldesc)
			}
		} else {
			// bad lvl desc from form
			dualMsg(&data.Msgs, syslog.LOG_ERR, "Bad OpenSIPS xlog level: "+newlvldesc)
		}
	}
	// get current level (maybe just set)
	cmdout, err := exec.Command("opensips-cli", "-x", "mi", "xlog_level").Output()
	// chk ret code
	if err != nil {
		dualMsg(&data.Msgs, syslog.LOG_WARNING, "Failed get xlog level via OpenSIPS CLI: "+err.Error())
	} else {
		ml.mylog(syslog.LOG_DEBUG, "OpenSIPS CLI result: "+string(cmdout))
		// parse the status message from the JSON response
		type xlglvl struct {
			XLog_level int `json:"xLog level"`
		}
		var oresp xlglvl
		err = json.Unmarshal(cmdout, &oresp)
		// fail to parse?
		if err != nil {
			ml.mylog(syslog.LOG_WARNING, "Failed to parse results from OpenSIPS CLI: "+err.Error())
		} else {
			// store the current log level
			currlvl = strconv.Itoa(oresp.XLog_level)
		}
	}
	// check each from map
	for k, v := range desc2lvl {
		// if the current level, store lvl desc
		if v == currlvl {
			currlvldesc = k
		}
	}
	// select tag w/ options pull-down
	selecttag, err := template2html("selecttag", struct {
		Name  string
		Value any
		Rows  [][]any
	}{"log-lvl", currlvldesc, lvls})
	// button to appy new level
	applytag := template.HTML("<input id='log-apply' name='log-apply' type='submit' value='Apply'>")
	// add tags to form
	data.CustomItems = []any{custom{"OpenSIPS xLog Level", selecttag},
		custom{"Press 'Apply' to set new level", applytag}}
	return
}

/*
 * display live logs
 */
func livelogs(Form url.Values, data *dat) (retfrorm string, err error) {
	// form for default menu
	retfrorm = "form-table-custom"
	data.Nextpage = "advmenu-displaylivelogs"
	data.Thispage = "advmenu-displaylivelogs"
	data.Title2 = " - Live Logs"
	data.Btn2 = true
	data.Btn2val = "Cancel"
	// Lynx browser?
	lynx := strings.Contains(Form.Get("useragent"), "Lynx")
	// names of logs for select/option menu
	logs := map[string]string{"mmsgate.log": "/var/log/mmsgate.log",
		"opensips.log":     "/var/log/opensips.log",
		"Nginx access.log": "/var/log/nginx/access.log",
		"Nginx error.log":  "/var/log/nginx/error.log",
		"syslog":           "/var/log/syslog"}
	selectlog := [][]any{{"mmsgate.log"}, {"opensips.log"}, {"Nginx access.log"}, {"Nginx error.log"}, {"syslog"}}
	// get values from form and set defaults if missing
	log := Form.Get("livelog")
	if log == "" {
		log = "mmsgate.log"
	}
	logfile, ok := logs[log]
	if !ok {
		dualMsg(&data.Msgs, syslog.LOG_ERR, "Invalid log selected: "+log)
	}
	lines := Form.Get("livelines")
	if lines == "" {
		lines = "100"
	}
	duration := Form.Get("livedur")
	if duration == "" {
		duration = "5"
	}
	// setup custom tags for form
	logselecttag, err := template2html("selecttag", struct {
		Name  string
		Value any
		Rows  [][]any
	}{"livelog", log, selectlog})
	linesselecttag, err := template2html("selecttag", struct {
		Name  string
		Value any
		Rows  [][]any
	}{"livelines", lines, [][]any{{"100"}, {"500"}, {"1000"}}})
	durationselecttag, err := template2html("selecttag", struct {
		Name  string
		Value any
		Rows  [][]any
	}{"livedur", duration, [][]any{{"5"}, {"10"}, {"30"}, {"60"}, {"90"}, {"120"}}})
	applytag := template.HTML("<input id='log-apply' name='log-apply' type='submit' value='Apply'>")
	// form data for template later
	data.CustomItems = []any{custom{"Live log to display", logselecttag},
		custom{"Past lines to include", linesselecttag},
		custom{"Live duration minutes", durationselecttag},
		custom{"Press 'Apply' to change selection", applytag}}
	// can't do live log in Lynx
	if lynx {
		data.Msgs = append(data.Msgs, template.HTML("Please use log viewing console commands.  Lynx is not compatible with live logs."))
	} else {
		// javascript for live log
		js, err := template2html("livelog", duration)
		if err != nil {
			dualMsg(&data.Msgs, syslog.LOG_ERR, "Template error: "+err.Error())
		}
		// messages and the textarea for the log data
		data.Msgs = append(data.Msgs, template.HTML("Log limited to "+duration+" minutes.  Maximun is 2 hours."))
		data.Msgs = append(data.Msgs, template.HTML("For longer, please use log viewing console commands.<br><textarea id='log' readonly style='height:1000px;width:100%'></textarea>"))
		// create unique fifo for 'tail' to pipe to
		uuid := uuid.New().String()
		data.Passdata = "log-" + uuid
		_, err = exec.Command("bash", "-c", "mkdir -p -m 777 /tmp/fifodir && mkfifo -m 777 /tmp/fifodir/"+data.Passdata).Output()
		if err != nil {
			ml.mylog(syslog.LOG_ERR, "Error creating fifo: "+err.Error())
		}
		go func() {
			// not really creating, we'll just write to it
			file, err := os.Create("/tmp/fifodir/" + data.Passdata)
			if err != nil {
				ml.mylog(syslog.LOG_ERR, "Error opening fifo: "+err.Error())
			}
			// close later
			defer func() {
				file.Close()
			}()
			// duration in seconds
			iduration, _ := strconv.Atoi(duration)
			dursec := strconv.Itoa(iduration * 60)
			// kick off log reader piping to fifo
			cmd := exec.Command("sudo", "/scripts/displaylog.sh", "/tmp/fifodir/"+data.Passdata, logfile, dursec, lines)
			cmd.Stdout = file
			cmd.Run()
		}()
		// append the javascript
		data.Msgs = append(data.Msgs, js)
	}
	return
}

/*
 * run the command and provide stdout results via channel
 */
func rsltfeed(cmd *exec.Cmd, ch chan<- []byte) {
	stdout, _ := cmd.StdoutPipe()
	// defer stdout.Close()
	cmd.Start()
	ml.mylog(syslog.LOG_DEBUG, "Started cmd: "+cmd.Path+" "+strings.Join(cmd.Args, " "))
	// collect results as it does
	rslt := []byte{}
	// var done bool
	// keep looping until eof
	for {
		// read some from stdout of test script
		buf := make([]byte, 1024)
		n, e := stdout.Read(buf)
		// if we got some, append to result
		if n > 0 {
			rslt = append(rslt, buf[:n]...)
			ml.mylog(syslog.LOG_DEBUG, "Read output: "+string(buf[:n]))
		}
		// error? must be eof
		if e != nil {
			ml.mylog(syslog.LOG_DEBUG, "Error: "+e.Error())
			// assume eof
			break
		}
		ml.mylog(syslog.LOG_DEBUG, "Sending progress...")
		// send results/progress via channel...
		ch <- rslt
	}
	// clean up resources and append exit status
	if err := cmd.Wait(); err != nil {
		if exiterr, ok := err.(*exec.ExitError); ok {
			rslt = append(rslt, ([]byte)(fmt.Sprintf("\nExit Status: %d", exiterr.ExitCode()))...)
		} else {
			rslt = append(rslt, ([]byte)(fmt.Sprintf("\ncmd.Wait: %v", err))...)
		}
	} else {
		rslt = append(rslt, ([]byte)("\nExit Status: 0")...)
	}
	// flag done
	rslt = append(rslt, 4)
	ml.mylog(syslog.LOG_DEBUG, "Sending final...")
	// final
	ch <- rslt
	//close(ch)
}

/*
 * generate the wizard forms and config settings
 */
func wizard(Form url.Values, data *dat, wizi int) (retfrorm string, reterr error) {
	retfrorm = "form-table-custom"
	// if panic,print/log details
	defer func() {
		if err := recover(); err != nil {
			strerr := HandleErrorWithLines(err.(error))
			reterr = errors.New(strerr)
		}
	}()
	// some initial form values
	data.CustomItems = []any{}
	data.Btn1val = "Back"
	data.Btn1 = true
	data.Btn2val = "Next"
	data.Btn2 = true
	data.Thispage = "wizmenu-" + strconv.Itoa(wizi)
	data.Nextpage = "wizmenu-" + strconv.Itoa(wizi+1)
	data.Title2 = " - Configuration Wizard"
	lynx := strings.Contains(Form.Get("useragent"), "Lynx")
	// need this for getitng ip address info
	type ip struct {
		Ipv4 struct {
			If     string
			Mac    string
			Local  string
			Public string
		}
		Ipv6 struct {
			If     string
			Mac    string
			Local  string
			Public string
		}
	}
	// returns struct w/ ip address info
	get_ip := func() (ipaddr ip, err error) {
		out, err := exec.Command("/scripts/getaddr.sh", "-j").Output()
		if err == nil {
			json.Unmarshal(out, &ipaddr)
		}
		return
	}
	switch wizi {
	// intro
	case 1:
		data.Msgs = append(data.Msgs, template.HTML("This is the MMSGate Wizard.  It will help you configure the Docker container and local network.  "+
			"Select \"Back\" at any time to return to the previous page.  "+
			"Click \"Next\" to continue.  This will stop OpenSIPS and disable it."))
	// disable opensips and get inot router
	case 2:
		set_global("ENABLEOPENSIPS", "N")
		out, err := exec.Command("sudo", "/scripts/stopopensips.sh").Output()
		if err != nil {
			data.Msgs = append(data.Msgs, template.HTML("Error stopping OpenSIPS.  Result: "+string(out)))
		}
		ip, err := get_ip()
		if err != nil {
			data.Msgs = append(data.Msgs, template.HTML("Error getting IP address info.  Error: "+err.Error()))
		}
		data.CustomItems = []any{custom{"Public IP address", template.HTML("<input readonly type='text' value='" + ip.Ipv4.Public + "'>")}}
		data.Msgs = append(data.Msgs, template.HTML("WARNING: MMSGate2 cannot determine the internal local host IP address or the router internal local IP address.  You must find them.  "+
			"For Windows, open a command prompt on the host and type 'ipconfig /all'. For Mac host, select Apple -> About this Mac ->System Report -> Network.  "+
			"For Linux, open a command prompt and type 'ip -4 addr' for IPv4 address and 'ip -0 addr' to find the Physical Address (i.e. MAC address).<br>"+
			"Make note of the IPv4 Address and the Physical Address (i.e. MAC address)."))
		data.Msgs = append(data.Msgs, template.HTML("Login to your local router using your web browser.  "+
			"If you have trouble and you purchased your router, perform an internet search of the make and model of the router.  If your router was provided by your ISP, "+
			"try contacting them for support.  Select \"Next\" once you are logged in."))
	// dhcp reserve
	case 3:
		data.Msgs = append(data.Msgs, template.HTML("In your router, you should reserve this host's IPv4 address (noted earlier) so it will not change.  "+
			"It is associated with the MAC address (also noted earlier) of this host.  In most routers, it is in the DHCP section and called \"static\" or \"reserved\".  "+
			"If the IP address is not properly reserved, the router may assign this host a different IPv4 address after the next power cycle.  That would cause issues with IPv4.  "+
			"This setting cannot be tested without powering off this host and the router for a significant amount of time.  Thus, this wazard will not test the IPv4 reservation.  "+
			"If you have trouble and you purchased your router, perform an internet search of the make and model of the router.  If your router was provided by your ISP, "+
			"try contacting them for support.  If this host is your router, this step can be skipped.  Select \"Next\" once done."))
	// port forward
	case 4:
		data.Msgs = append(data.Msgs, template.HTML("In your router, you need to configure IPv4 port forwarding.  The router needs to forward IPv4 TCP/IP packets from the Internet to this host's local IPv4 address "+
			"noted earlier.  Two TCP ports are needed, 5061 and 38443.  The port forward settings are usually in the firewall or advanced section of the router settings.  "+
			"If you have trouble and you purchased your router, perform an internet search of the make and model of the router.  If your router was provided by your ISP, "+
			"try contacting them for support.  The configuration will be tested.  If this host is your router, then it will be firewall traffic rules, not port forward.  "+
			"Select \"Next\" once done."))
	// prompt for fw test
	case 5:
		var proxy string
		// get a random proxy url
		resp, err := http.Get("http://pubproxy.com/api/proxy?country=US,CA&type=http")
		if err != nil {
			data.Msgs = append(data.Msgs, template.HTML("Error getting random proxy from pubproxy.com.  Error: "+err.Error()))
		} else {
			// read in the entire response
			body, err := io.ReadAll(resp.Body)
			defer resp.Body.Close()
			if err != nil {
				data.Msgs = append(data.Msgs, template.HTML("Error reading response from pubproxy.com.  Error: "+err.Error()))
			} else {
				ml.mylog(syslog.LOG_DEBUG, "pubproxy.com API response body: "+string(body))
				// parse the status message from the JSON response
				type jresp struct {
					Data []struct {
						Type   string
						IpPort string
					}
					Count int
				}
				var oresp jresp
				err = json.Unmarshal(body, &oresp)
				if err != nil || oresp.Count == 0 || resp.StatusCode != 200 {
					data.Msgs = append(data.Msgs, template.HTML("Error response from pubproxy.com. Status: "+resp.Status+"  Response: "+string(body)))
				} else {
					// okay, use this proxy as default
					proxy = oresp.Data[0].Type + "://" + oresp.Data[0].IpPort
				}
			}
		}
		data.CustomItems = []any{custom{"HTTP proxy URL", template.HTML("<input id='proxy' name='proxy' type='text' value='" + proxy + "'>")},
			custom{"Use Tor", template.HTML("<input id='tor' name='tor' type='checkbox' value='tor'>")}}
		data.Msgs = append(data.Msgs, template.HTML("Network connectivity will now be tested for remote and local access to this container via the public IP addresses.  "+
			"The remote test uses a free unauthenticated http proxy service.  Many are available, but they often come and go and may be unreliable.  "+
			"You can find your own via a <a href='https://www.google.com/search?q=free+unauthenticated+http+proxy+service' target='_blank'>Google</a> search.  "+
			"If the above entered http proxy fails, alternates from pubproxy.com will be tried.  "+
			"If you select Tor, the Tor services will be used.  "+
			"However, Tor needs more memory than the usual 100m granted to the container.  For Tor, 200m or more is recommended.  "+
			"Select \"Next\" to begin test."))
	// perform fw test
	case 6:
		// test in progress or not started?
		if chfw == nil {
			// no go routine...  need to start a test!
			testscript := "/scripts/fwtestviaproxy.sh"
			if Form.Get("tor") == "tor" {
				testscript = "/scripts/fwtestviator.sh"
			}
			proxy := Form.Get("proxy")
			// and come back here
			data.Nextpage = "wizmenu-6"
			// test for public ip
			ipaddr, err := get_ip()
			if err == nil {
				// kick off the test in the background
				data.Msgs = append(data.Msgs, template.HTML("Testing started...  Wait a few more seconds and click \"Next\"."))
				chfw = make(chan []byte)
				// kick off test script
				cmd := exec.Command("sudo", "--preserve-env=HTTP_PROXY", testscript, ipaddr.Ipv4.Public+":5061", ipaddr.Ipv4.Public+":38443")
				cmd.Env = append(cmd.Env, "HTTP_PROXY="+proxy)
				go rsltfeed(cmd, chfw)
			} else {
				dualMsg(&data.Msgs, syslog.LOG_ERR, "Error getting public IP address.  Error: "+err.Error())
			}
		} else {
			// test is in progress
			var rslt []byte
			var done, ok bool
			// query twice to get latest progress
			for range 2 {
				if rslt, ok = <-chfw; !ok {
					dualMsg(&data.Msgs, syslog.LOG_ERR, "Error getting progress from channel.  Not ok.")
				}
				if rslt[len(rslt)-1] == 4 {
					rslt = rslt[:len(rslt)-1]
					chfw = nil
					done = true
					break
				}
			}
			// test all done?
			if done {
				// test successful?
				if bytes.Contains(rslt, ([]byte)("Congratulations!")) {
					data.Msgs = append(data.Msgs, template.HTML("Success!  Select \"Next\" to continue."))
				} else {
					// failed, go back to try again
					data.Nextpage = "wizmenu-4"
					data.Msgs = append(data.Msgs, template.HTML("Failed!  Select \"Next\" to try again."))
				}
			} else {
				// still waiting...  come back here to check progress
				data.Nextpage = "wizmenu-6"
				data.Msgs = append(data.Msgs, template.HTML("Testing still in progress...  Wait a few more seconds and click  \"Next\"."))
			}
			// display progress so far, if any
			if len(rslt) > 0 {
				data.Msgs = append(data.Msgs, template.HTML("Details:<br>"+string(bytes.ReplaceAll(rslt, []byte{'\n'}, ([]byte)("<br>")))))
			}
		}
	// signup at dynu.com
	case 7:
		// get apikey if already set
		apikey, _ := get_global("DNSTOKEN")
		// prompt for dnstoken/apikey
		data.CustomItems = []any{custom{"API Key", template.HTML("<input id='apikey' name='apikey' type='text' value='" + apikey + "'>")}}
		msg := "You need to sign up with a free Dynamic Domain Name System (DDNS) service.  The tested and supported free provider is Dynu at the "
		if lynx {
			msg += "https://dynu.com"
		} else {
			msg += "<a href='https://dynu.com' target='_blank'>https://dynu.com</a>"
		}
		msg += " web site.  Visit their web site and under the DDNS menu, select sign up.  For option 1, type a preferred host name.  Examples would be \"gregsmmsgate\" or \"sallysopensips\".  " +
			"Select a different top level domain if desired.  Click add.  You will be prompted to create an account.  Fill out the prompts as needed, making note of your username and password.  " +
			"Click submit and perform the verifications as needed.  Once verified and logged into Dynu, click the gears in the upper-right of Dynu's web page.  " +
			"Click \"API Credentials\".  In the list of existing API Credentials, to the right of  \"API Key\", click the view (binoculars) button.  " +
			"The key will appear as a long string of random characters.  Highlight it and copy it to your clipboard.  Paste it into the prompt of the above dialog.  "
		if lynx {
			msg += "To paste into this SSH session, you may need to right-click.  "
		}
		msg += "Select \"Next\" once you have filled in the API Key."
		data.Msgs = append(data.Msgs, template.HTML(msg))
	// pick dns name
	case 8:
		// get dns name if already set
		dnsname, _ := get_global("DNSNAME")
		//
		apikey := Form.Get("apikey")
		if apikey == "" {
			apikey, _ = get_global("DNSTOKEN")
		}
		// blank?
		if apikey == "" {
			data.Msgs = append(data.Msgs, template.HTML("Invalid or missing API key.  select \"Next\" to try again."))
			data.Nextpage = "wizmenu-7"
		} else {
			// get dns names
			req, err := http.NewRequest("GET", "https://api.dynu.com/v2/dns", nil)
			req.Header.Add("accept", "application/json")
			req.Header.Add("API-Key", apikey)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				dualMsg(&data.Msgs, syslog.LOG_ERR, "Error returned from dynu.com API. Error: "+err.Error())
				data.Msgs = append(data.Msgs, template.HTML("Invalid or missing API key.  select \"Next\" to try again."))
				data.Nextpage = "wizmenu-7"
			} else {
				// read in the entire response
				body, err := io.ReadAll(resp.Body)
				defer resp.Body.Close()
				if err != nil || resp.StatusCode != 200 {
					dualMsg(&data.Msgs, syslog.LOG_ERR, "Error trying to read dynu.com API response. Http status: "+strconv.Itoa(resp.StatusCode)+" Error: "+err.Error())
					data.Msgs = append(data.Msgs, template.HTML("Invalid or missing API key.  select \"Next\" to try again."))
					data.Nextpage = "wizmenu-7"
				} else {
					ml.mylog(syslog.LOG_DEBUG, "dynu.com API response body: "+string(body))
					// parse the status message from the JSON response
					type jresp struct {
						Domains []struct {
							Name string
						}
					}
					var oresp jresp
					err = json.Unmarshal(body, &oresp)
					if err != nil {
						dualMsg(&data.Msgs, syslog.LOG_ERR, "Error trying to parse dynu.com API response.  Error: "+err.Error())
						data.Msgs = append(data.Msgs, template.HTML("Invalid or missing API key.  select \"Next\" to try again."))
						data.Nextpage = "wizmenu-7"
					} else {
						// make form for selecting dns name
						if len(oresp.Domains) > 0 {
							doms := [][]any{}
							for _, d := range oresp.Domains {
								doms = append(doms, []any{d.Name})
							}
							// select tag w/ options pull-down
							selecttag, err := template2html("selecttag", struct {
								Name  string
								Value any
								Rows  [][]any
							}{"dnsname", dnsname, doms})
							if err != nil {
								dualMsg(&data.Msgs, syslog.LOG_ERR, "Error trying to process dynu.com API response.  Error: "+err.Error())
								data.Nextpage = "wizmenu-7"
							} else {
								data.CustomItems = []any{custom{"DDNS Name", selecttag}}
								data.Msgs = append(data.Msgs, template.HTML("The API Key was entered correctly and access was successful!  "+
									"Please select the DDNS name you want to use from the above pull-down.  Select \"Next\" when ready."))
								set_global("DNSTOKEN", apikey)
							}
						} else {
							data.Msgs = append(data.Msgs, template.HTML("Error!  The API Key was entered correctelly and access was successful!  "+
								"However, no DDNS host names were found.  "+
								"Please add one or more from the Dynu web site by clicking the gears in the upper-right, "+
								"then \"DDNS Services\" and click \"+Add\".  Once done, select \"Next\" here to try again."))
							set_global("DNSTOKEN", apikey)
							data.Nextpage = "wizmenu-8"
						}
					}
				}
			}
		}
	// cert intro
	case 9:
		// get dns name
		dnsname := Form.Get("dnsname")
		if dnsname == "" {
			dnsname, _ = get_global("DNSNAME")
		}
		// good name
		if dnsname != "" {
			// save it
			set_global("DNSNAME", dnsname)
			data.Msgs = append(data.Msgs, template.HTML("The DDNS name "+dnsname+" was selected and will be used."))
			// prompt for email address for certs
			email, _ := get_global("EMAIL")
			data.CustomItems = []any{custom{"eMail Address", template.HTML("<input id='email' name='email' type='text' value='" + email + "'>")}}
			data.Msgs = append(data.Msgs, template.HTML("A certificate is required.  It allows secure encrypted communications.  "+
				"Let's Encrypt requests an email address for issuing a free certificate.  "+
				"Enter your email address in the above dialog.  Once entered, a certificate will be requested.  This container will automatically "+
				"request renewal of the certificate 30 days before the expiration.  Select \"Next\" when ready."))
		} else {
			data.Msgs = append(data.Msgs, template.HTML("A DDNS name was not selected.  Select \"Next\" to try again."))
			data.Nextpage = "wizmenu-8"
		}
	// cert generate
	case 10:
		// no cert gen in progress?
		if chcrt == nil {
			// get email
			email := Form.Get("email")
			if email == "" {
				email, _ = get_global("EMAIL")
			}
			// good email?
			if email != "" {
				// save it
				data.Msgs = append(data.Msgs, template.HTML("The eMail address "+email+" was entered and will be used."))
				set_global("EMAIL", email)
				// kick off cert gen
				chcrt = make(chan []byte)
				cmd := exec.Command("sudo", "/scripts/certs.sh", "-o")
				go rsltfeed(cmd, chcrt)
				data.Msgs = append(data.Msgs, template.HTML("A certificate request is in progress.  Wait a few seconds and press \"Next\" to see results."))
				data.Nextpage = "wizmenu-10"
			} else {
				data.Msgs = append(data.Msgs, template.HTML("Error! The eMail address is missing or bad.<br>Select \"Next\" to try again."))
				data.Nextpage = "wizmenu-9"
			}
			// cert gen is already in progress
		} else {
			var rslt []byte
			var done, ok bool
			// query twice to get latest progress
			for range 2 {
				if rslt, ok = <-chcrt; !ok {
					dualMsg(&data.Msgs, syslog.LOG_ERR, "Error getting progress from channel.  Not ok.")
				}
				// got end trans?
				if rslt[len(rslt)-1] == 4 {
					// remove end trans
					rslt = rslt[:len(rslt)-1]
					// we are done
					chcrt = nil
					done = true
					break
				}
			}
			// cert all done?
			if done {
				// cert successful?
				if bytes.Contains(rslt, ([]byte)("Exit Status: 0")) {
					data.Msgs = append(data.Msgs, template.HTML("Success!  Select \"Next\" to continue."))
				} else {
					data.Msgs = append(data.Msgs, template.HTML("Certificate request failed!  Select \"Next\" to try again."))
					data.Nextpage = "wizmenu-9"
				}
			} else {
				data.Msgs = append(data.Msgs, template.HTML("A certificate request is already in progress.  Wait a few more seconds and press \"Next\" to see results."))
				data.Nextpage = "wizmenu-10"
			}
			// display progress so far, if any
			if len(rslt) > 0 {
				data.Msgs = append(data.Msgs, template.HTML("Details:<br>"+string(bytes.ReplaceAll(rslt, []byte{'\n'}, ([]byte)("<br>")))))
			}
		}
	// config Voip.ms API
	case 11:
		// get dns name for voip.ms page config
		dnsname, _ := get_global("DNSNAME")
		// prompt for api id/pw
		voipid, _ := get_global("APIID")
		voippw, _ := get_global("APIPW")
		data.CustomItems = []any{custom{"Voip.ms User ID", template.HTML("<input id='voipid' name='voipid' type='text' value='" + voipid + "'>")},
			custom{"Voip.ms API Password", template.HTML("<input id='voippw' name='voippw' type='password' value='" + voippw + "'>")}}
		m := "The Voip.ms API must be enabled.  Logon to the "
		if lynx {
			m += "https://voip.ms"
		} else {
			m += "<a href='https://voip.ms' target='_blank'>https://voip.ms</a>"
		}
		m += " web site and select \"Main Menu->SOAP and REST/JSON API\".  If API is not already enabled, " +
			"click \"Enable/Disable API\".  Enter, confirm and make note of an API password, click \"Save API Password\".  For \"Enable IP Address\", " +
			"paste \"" + dnsname + "\" and click \"Save IP Addresses\".  Enter your Voip.ms ID and the API password above.  " +
			"Select \"Next\" here when done and ready to provide credentials to MMSGate. "
		data.Msgs = append(data.Msgs, template.HTML(m))
	// Voip.ms test
	case 12:
		// get api id/pw
		voipid := Form.Get("voipid")
		voippw := Form.Get("voippw")
		if voipid == "" {
			voipid, _ = get_global("APIID")
		}
		if voippw == "" {
			voippw, _ = get_global("APIPW")
		}
		// test them
		url := fmt.Sprintf("https://voip.ms/api/v1/rest.php?api_username=%s&api_password=%s&method=getSubAccounts", voipid, url.QueryEscape(voippw))
		_, err := voip_api(url)
		// worked?
		if err == nil {
			// worked!
			data.Msgs = append(data.Msgs, template.HTML("The ID and password tested successfully.  Select \"Next\" to continue."))
			set_global("APIID", voipid)
			set_global("APIPW", voippw)
		} else {
			// failed!
			data.Msgs = append(data.Msgs, template.HTML("The ID and/or password failed.  Select \"Next\" to try again."))
			data.Nextpage = "wizmenu-11"
		}
	// restart and done!
	case 13:
		// message
		data.Msgs = append(data.Msgs, template.HTML("Congratulations!  MMSGate is now configured.  The next steps are to optionally add Linphone accounts for push notifications.  "+
			"Push notification is optional.  Also set MMSGate preferences for the Voip.ms sub accounts.  Then finally configure clients.  "+
			"Note: Configuring clients is done from the sub accounts menu.<br>"+
			"OpenSIPS has been enabled and the container re-started. Select \"Next\" to return to the main menu."))
		// enable opensips
		set_global("ENABLEOPENSIPS", "Y")
		// go abck to mainmenu
		data.Nextpage = "mainmenu"
		// restart container
		cmd := exec.Command("sudo", "/scripts/restart.sh")
		cmd.Start()
	}
	return
}

/*
 * display/modify mmsgate2 log level
 */
func setgateloglvl(Form url.Values, data *dat) (retfrorm string, err error) {
	// form for default menu
	retfrorm = "form-table-custom"
	data.Nextpage = "advmenu-setmmsgateloglevel"
	data.Thispage = "advmenu-setmmsgateloglevel"
	data.Title2 = " - Set MMSGate2 Log Level"
	data.Btn2 = true
	data.Btn2val = "Cancel"
	// did they hit 'Apply'?
	if Form.Get("log-apply") == "Apply" {
		// get new log lvl
		newlvl, err := ml.str2lvl(Form.Get("log-lvl"))
		// good lvl?
		if err != nil {
			dualMsg(&data.Msgs, syslog.LOG_ERR, "Invalid MMSgate log level: "+Form.Get("log-lvl"))
		} else {
			// apply it
			ml.gsysLogPri = newlvl
			dualMsg(&data.Msgs, syslog.LOG_ALERT, "New MMSGate2 log level applied as: "+Form.Get("log-lvl"))
		}
	}
	// get current level (maybe just set)
	curlvl := ml.gsysLogMapRev[ml.gsysLogPri]
	// build pull-down select for level
	lvls := [][]any{}
	for l := syslog.LOG_EMERG; l <= syslog.LOG_DEBUG; l++ {
		lvls = append(lvls, []any{ml.gsysLogMapRev[l]})
	}
	// select tag w/ options pull-down
	selecttag, err := template2html("selecttag", struct {
		Name  string
		Value any
		Rows  [][]any
	}{"log-lvl", curlvl, lvls})
	// button to appy new level
	applytag := template.HTML("<input id='log-apply' name='log-apply' type='submit' value='Apply'>")
	// add tags to form
	data.CustomItems = []any{custom{"MMSGate2 Log Level", selecttag},
		custom{"Press 'Apply' to set new level", applytag}}
	return
}

/*
 * display/modify mmsgate2 log level
 */
func setglobaldebug(Form url.Values, data *dat) (retfrorm string, err error) {
	// form for default menu
	retfrorm = "form-table-custom"
	data.Nextpage = "advmenu-setglobaldebug"
	data.Thispage = "advmenu-setglobaldebug"
	data.Title2 = " - Set Global Debug"
	data.Btn2 = true
	data.Btn2val = "Cancel"
	// current value
	gdbg, _ := get_global("DEBUG")
	// did they hit 'Apply'?
	if Form.Get("gdebug-apply") == "Apply" {
		if Form.Get("gdebug") == "" {
			if gdbg == "N" {
				dualMsg(&data.Msgs, syslog.LOG_DEBUG, "Global Debug already disabled.")
			} else {
				dualMsg(&data.Msgs, syslog.LOG_DEBUG, "Global Debug has been disabled.")
				set_global("DEBUG", "N")
			}
		} else {
			if gdbg == "Y" {
				dualMsg(&data.Msgs, syslog.LOG_DEBUG, "Global Debug already enabled.")
			} else {
				dualMsg(&data.Msgs, syslog.LOG_DEBUG, "Global Debug has been enabled.")
				set_global("DEBUG", "Y")
			}
		}
	}
	gdbg, _ = get_global("DEBUG")
	chk := ""
	if gdbg == "Y" {
		chk = "checked"
	}
	// check box tag
	checkboxtag := template.HTML("<input id='gdebug' name='gdebug' type='checkbox' " + chk + " value='gdebug'>")
	// button to appy new level
	applytag := template.HTML("<input id='gdebug-apply' name='gdebug-apply' type='submit' value='Apply'>")
	// add tags to form
	data.CustomItems = []any{custom{"Enable Global Debug", checkboxtag},
		custom{"Press 'Apply' to set new level", applytag}}
	return
}

/*
 * set admin password
 */
func setadminpassword(Form url.Values, data *dat) (retfrorm string, err error) {
	// form for default menu
	retfrorm = "form-table-custom"
	data.Nextpage = "advmenu-setadminpassword"
	data.Thispage = "advmenu-setadminpassword"
	data.Title2 = " - Set Admin Password"
	data.Btn2 = true
	data.Btn2val = "Cancel"
	// hit apply?
	if Form.Get("pw-apply") == "Apply" {
		// get pw from submitted form
		pw1 := Form.Get("password1")
		pw2 := Form.Get("password2")
		// blank?
		if pw1 == "" {
			data.Msgs = append(data.Msgs, template.HTML("Password cannot be blank."))
		} else {
			// not matching
			if pw1 != pw2 {
				data.Msgs = append(data.Msgs, template.HTML("Password and confirmation do not match."))
			} else {
				// change it
				// rslt, err := exec.Command("bash", "-c", "echo -n \"admin:\" > /etc/opensips/nginx/.htpasswd && openssl passwd -apr1 \""+pw1+"\" >> /etc/opensips/nginx/.htpasswd").Output()
				cmd := exec.Command("bash", "-c", "echo -n \"admin:\" > /etc/opensips/nginx/.htpasswd && openssl passwd -apr1 \"$NEWPW\" >> /etc/opensips/nginx/.htpasswd")
				cmd.Env = append(cmd.Env, "NEWPW="+pw1)
				rslt, err := cmd.Output()
				if err != nil {
					dualMsg(&data.Msgs, syslog.LOG_ERR, "Failed to change password: "+err.Error()+"\n"+string(rslt))
				} else {
					dualMsg(&data.Msgs, syslog.LOG_ERR, "Admin password changed.")
				}
			}
		}
	}
	// prompt for new password
	password1tag := template.HTML("<input id='password1' name='password1' type='password' value=''>")
	password2tag := template.HTML("<input id='password2' name='password2' type='password' value=''>")
	// button to appy new level
	applytag := template.HTML("<input id='pw-apply' name='pw-apply' type='submit' value='Apply'>")
	// add tags to form
	data.CustomItems = []any{custom{"Enter new admin password", password1tag},
		custom{"Re-enter new admin password to confirm", password2tag},
		custom{"Press 'Apply' to set new password", applytag}}
	return
}

/*
 * restart the container
 */
func restart(Form url.Values, data *dat) (retfrorm string, err error) {
	// form for default menu
	retfrorm = "form-table-custom"
	data.Nextpage = "advmenu-restart"
	data.Thispage = "advmenu-restart"
	data.Title2 = " - Restart Container"
	data.Btn2 = true
	data.Btn2val = "Cancel"
	// hit apply?
	if Form.Get("restart-apply") == "Apply" {
		if Form.Get("restart") != "" {
			dualMsg(&data.Msgs, syslog.LOG_ALERT, "Restart requested.")
			data.Msgs = append(data.Msgs, template.HTML("Please wait 30 seconds before pressing Cancel to return to main menu."))
			cmd := exec.Command("sudo", "/scripts/restart.sh")
			// launch and forget...  cmd.Wait() not needed since container will restart
			cmd.Start()
		} else {
			data.Msgs = append(data.Msgs, template.HTML("To restart, check the checkbox and press Apply."))
		}
	}
	checkboxtag := template.HTML("<input id='restart' name='restart' type='checkbox' value='restart'>")
	// button to restart
	applytag := template.HTML("<input id='restart-apply' name='restart-apply' type='submit' value='Apply'>")
	data.CustomItems = []any{custom{"Check this box to confirm restart", checkboxtag},
		custom{"Press 'Apply' to restart the container", applytag}}
	return
}

var (
	/*
	 * global for DB connection
	 */
	db *sql.DB
	/*
	 * global used to bump send msgs go routine and to tell it exit
	 */
	c chan bool
	/*
	 * global used to pass firewall test results
	 */
	chfw chan []byte
	/*
	 * global used to pass cert gen results
	 */
	chcrt chan []byte
	/*
	 * global syslog object
	 */
	ml *myLogger
	/*
	 * global html templates
	 */
	tmpl map[string]*template.Template
)

/*
 * Start here
 */
func main() {
	// custom logger setup
	slvl := os.Getenv("LOGLVL")
	lvl, err := ml.str2lvl(slvl)
	// did we get a good converion of ENV LOGLVL to a syslog.priority?
	if err == nil {
		ml = new(myLogger).init(lvl).mylog(syslog.LOG_ALERT, "Started up... Log Level: "+slvl)
	} else {
		// no, we did not get good conversion.  defaulting to WARNING level
		ml = new(myLogger).init(lvl).mylog(syslog.LOG_ALERT, "Started up... Log Level: WARNING (default)")
	}
	// if panic,print/log details and exit to os
	defer func() {
		if err := recover(); err != nil {
			HandleErrorWithLines(err.(error))
			os.Exit(1)
		}
	}()
	// Env Var defaults
	for k, v := range map[string]string{"LOGLVL": "WARNING",
		"BINDOVERRIDE": "127.0.0.1:38080",
		"GLOBALCFG":    "/etc/opensips/globalcfg.sh",
		"DBPATH":       "/data/opensips/opensips.sqlite",
		"DBPATHM":      "/data/mmsgate/mmsgate.sqlite",
		"PATHGET":      "/mmsmedia",
		"PROTOCOL":     "https",
		"WEBPORT":      "38443",
		"LOCALMEDIA":   "/data/mmsmedia",
		"RECODAYS":     "7",
		"RECOHRS":      "6",
		"APIKEYHRS":    "5",
		"DEBUGCON":     "N",
		"PATHFILE":     "/file",
		"PATHADMIN":    "/admin",
		"PATHGATE":     "/mmsgate",
		"PATHTMPL":     "/scripts/tmpl"} {
		// get current
		evalue := os.Getenv(k)
		// blank? missing?
		if evalue == "" {
			// set the default
			os.Setenv(k, v)
			ml.mylog(syslog.LOG_WARNING, "Environment variable '"+k+"' missing or bad.  Using default '"+v+"' value.")
		}
	}
	// Setup DB
	db = get_dbconn()
	defer db.Close()
	init_linphonedb()
	init_subacctdb()
	init_msgdb()
	// load the sub accts dbfrom the VoIP.ms API
	msg := pop_subacctdb()
	for _, m := range msg {
		ml.mylog(syslog.LOG_WARNING, "Msg from populate sub acct func: "+m)
	}
	// run reconcile on regular schedule
	go sched_reconcile()
	// run apikeychecks on regular schedule
	go sched_apikeychecks()
	// start send msg go routine
	c = make(chan bool, 10)
	go send_msgs(c)
	// first bump
	c <- true
	// load the html templates needed for admin pages
	tmpl = make(map[string]*template.Template)
	pathtmpl := os.Getenv("PATHTMPL")
	tmpl["form-top"] = template.Must(template.ParseFiles(pathtmpl+"/form-top.html", pathtmpl+"/base.html"))
	tmpl["form-table-menu"] = template.Must(template.ParseFiles(pathtmpl+"/form-table-menu.html", pathtmpl+"/form-top.html", pathtmpl+"/base.html"))
	tmpl["form-table-custom"] = template.Must(template.ParseFiles(pathtmpl+"/form-table-custom.html", pathtmpl+"/form-top.html", pathtmpl+"/base.html"))
	tmpl["form-table-2d"] = template.Must(template.ParseFiles(pathtmpl+"/form-table-2d.html", pathtmpl+"/form-top.html", pathtmpl+"/base.html"))
	tmpl["livelog"] = template.Must(template.ParseFiles(pathtmpl + "/livelog.js"))
	tmpl["selecttag"] = template.Must(template.New("selecttag").Parse("<select id='{{ .Name }}' name='{{ .Name }}'>{{ range .Rows }}{{ with $v := index . 0 }} <option{{ if (eq $v $.Value ) }} selected{{ end }} value='{{ $v }}'>{{ $v }}</option>{{ end }}{{ end }}</select>"))
	tmpl["vcard"] = template.Must(template.New("vcard").Parse("{{ range . }}BEGIN:VCARD\r\nVERSION:4.0\nKIND:individual\nIMPP:sips:{{ .ext }}@{{ .domain }}\nFN:{{ .fn }}\nUID:urn:uuid:{{ .uuid }}\nEND:VCARD\n{{ end }}"))
	// web handlers
	pathgate := os.Getenv("PATHGATE")
	http.HandleFunc(pathgate, webhookHandler)
	pathfile := os.Getenv("PATHFILE")
	http.HandleFunc(pathfile, uploadHandler)
	pathadmin := os.Getenv("PATHADMIN")
	http.HandleFunc(pathadmin, adminHandler)
	// start http server
	bind := os.Getenv("BINDOVERRIDE")
	err = http.ListenAndServe(bind, nil)
	// we are done - should never get here...
	if err != nil {
		ml.mylog(syslog.LOG_ERR, "http.ListenAndServe returned error: "+err.Error())
	}
	c <- false
	ml.mylog(syslog.LOG_ALERT, "Exiting: "+err.Error())
}
