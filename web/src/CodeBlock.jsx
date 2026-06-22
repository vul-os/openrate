import React from "react";

// Minimal dependency-free JSON syntax highlighter.
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
      } else out.push(<span className="tok-str" key={i++}>{m[1]}</span>);
    } else if (m[3] !== undefined) out.push(<span className="tok-num" key={i++}>{m[3]}</span>);
    else if (m[4] !== undefined) out.push(<span className="tok-bool" key={i++}>{m[4]}</span>);
    else if (m[5] !== undefined) out.push(<span className="tok-punct" key={i++}>{m[5]}</span>);
    last = re.lastIndex;
  }
  if (last < code.length) out.push(code.slice(last));
  return out;
}

// Light shell highlighter: comments muted, prompts faint, flags accented.
function highlightShell(code) {
  return code.split("\n").map((line, i) => {
    const t = line.trimStart();
    if (t.startsWith("#")) return <div key={i} className="tok-comment">{line || " "}</div>;
    // colour a leading $ prompt
    const parts = /^(\s*\$\s*)(.*)$/.exec(line);
    if (parts) return <div key={i}><span className="tok-prompt">{parts[1]}</span>{parts[2] || " "}</div>;
    return <div key={i}>{line || " "}</div>;
  });
}

// CodeBlock — editor chrome (traffic lights + optional request line) over a
// highlighted body. lang: "json" (default) | "bash" | "text".
export default function CodeBlock({ title, method, code, lang = "json" }) {
  const body = lang === "json" ? highlightJSON(code) : lang === "bash" ? highlightShell(code) : code;
  return (
    <div className="codeblock">
      <div className="cb-bar">
        <span className="cb-dots"><i /><i /><i /></span>
        {title && <span className="cb-title">{method && <span className="cb-method">{method}</span>}{title}</span>}
      </div>
      <pre className="cb-body"><code>{body}</code></pre>
    </div>
  );
}
