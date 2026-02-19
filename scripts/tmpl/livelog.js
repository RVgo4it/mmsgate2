<script>
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
          e.innerHTML += "\nReached max {{ . }} minutes!";
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
</script>