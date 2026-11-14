// Used by parity_test.go. Reads cases worked out by the Go evaluator and works
// each one out again with the JavaScript engine the template ships.
"use strict";
var fs = require("fs");
require("../files/js/decimal.js");
require("../files/js/engine.js");
var input = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
var different = 0;
input.cases.forEach(function (c, i) {
  var got = globalThis.MoldloftEngine.evaluate(input.tool, c.raw);
  var same = JSON.stringify(got.values, Object.keys(got.values).sort()) === JSON.stringify(c.values, Object.keys(c.values).sort()) &&
    JSON.stringify(got.faults) === JSON.stringify(c.faults);
  if (!same) {
    different++;
    console.log("case " + i + " differs: " + JSON.stringify(c.raw));
  }
});
console.log(different === 0 ? "same " + input.cases.length : "different " + different);
process.exit(different === 0 ? 0 : 1);
