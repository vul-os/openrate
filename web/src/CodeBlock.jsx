import React from "react";

// Minimal dependency-free JSON syntax highlighter. Tokenises strings (keys vs
// values), numbers, booleans/null and punctuation into themed spans, preserving
// whitespace. Good enough for the small API examples we show.
function highlightJSON(code) {
  const re = /("(?:\\.|[^"\\])*")(\s*:)?|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|\b(true|false|null)\b|([{}\[\],])/g;
  const out = [];
  let last = 0, m, i = 0;
  while ((m = re.exec(code)) !== null) {
    if (m.index > last) out.push(code.slice(last, m.index));
    if (m[1] !== undefined) {
      if (m[2] !== undefined) {
        out.push(<span className="tok-key" key={i++}>{m[1]}</span>);
        out.push(<span className="tok-punct" key={i++}>{m[2]}</span>);
      } else {
        out.push(<span className="tok-str" key={i++}>{m[1]}</span>);
      }
    } else if (m[3] !== undefined) out.push(<span className="tok-num" key={i++}>{m[3]}</span>);
    else if (m[4] !== undefined) out.push(<span className="tok-bool" key={i++}>{m[4]}</span>);
    else if (m[5] !== undefined) out.push(<span className="tok-punct" key={i++}>{m[5]}</span>);
    last = re.lastIndex;
  }
  if (last < code.length) out.push(code.slice(last));
  return out;
}

// CodeBlock — editor chrome (traffic lights + optional request line) over a
// highlighted JSON body.
export default function CodeBlock({ title, method = "GET", code }) {
  return (
    <div className="codeblock">
      <div className="cb-bar">
        <span className="cb-dots"><i /><i /><i /></span>
        {title && <span className="cb-title"><span className="cb-method">{method}</span> {title}</span>}
      </div>
      <pre className="cb-body"><code>{highlightJSON(code)}</code></pre>
    </div>
  );
}
