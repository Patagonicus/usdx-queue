"use strict";

function update(url, cb) {
  var xhr = new XMLHttpRequest();
  xhr.open('GET', url, true);
  xhr.responseType = 'json';
  xhr.onload = function() {
    if (xhr.status === 200) {
      cb(xhr.response, null);
    } else {
      console.log("could not get state");
    }
    setTimeout(update, 1000, url, cb);
  };
  xhr.send();
}
