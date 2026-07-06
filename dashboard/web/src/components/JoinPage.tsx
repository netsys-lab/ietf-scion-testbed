import { marked } from "marked";
import QRCode from "qrcode";
import { useEffect, useRef, useState } from "react";
import { claimConf, fetchInstruction, fetchInstructions, fetchJoinMeta, type JoinMeta } from "../api";
import { confFilename, loadClaim, pickConf, saveClaim, type ClaimResult } from "../join";
import "../join.css";

export default function JoinPage() {
  const [meta, setMeta] = useState<JoinMeta | null>(null);
  const [metaErr, setMetaErr] = useState<string | null>(null);
  const [claim, setClaim] = useState<ClaimResult | null>(loadClaim());
  const [as, setAs] = useState<number | null>(null);
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [claimErr, setClaimErr] = useState<string | null>(null);
  const [v4, setV4] = useState(false);

  useEffect(() => {
    fetchJoinMeta().then(setMeta).catch((e) => setMetaErr(String(e)));
  }, []);

  async function doClaim() {
    if (as === null) return;
    setBusy(true);
    setClaimErr(null);
    try {
      const c = await claimConf(as, code);
      saveClaim(c);
      setClaim(c);
    } catch (e) {
      const s = (e as Error).message;
      setClaimErr(
        s === "403" ? "Wrong booth code." :
        s === "409" ? "All confs are claimed — ask at the booth." :
        s === "429" ? "Too many attempts — wait a minute." :
        "Join is unavailable right now.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="join">
      <header className="join-head">
        <h1>Join the SCION testbed</h1>
        <a href="/">← fabric map</a>
      </header>

      <section className="join-tier">
        <h2>Try it now — browser playground</h2>
        <p>A live SCION endhost shell, zero install. Booth code required.</p>
        <div className="join-cards">
          {(meta?.joinable_ases ?? [158, 159, 160, 161]).map((n) => (
            <a key={n} className="join-card" href={`/play/${n}/`}>AS 1-{n} terminal</a>
          ))}
        </div>
      </section>

      <section className="join-tier">
        <h2>Join with your laptop — WireGuard</h2>
        {metaErr && <p className="join-err">Join service unreachable.</p>}
        {meta && !meta.hub_ok && <p className="join-err">Hub offline — ask at the booth.</p>}
        {meta && (
          <p className="join-slots">{meta.slots_total - meta.slots_claimed - meta.slots_burned} of {meta.slots_total} confs free</p>
        )}
        {!claim && meta && (
          <div className="join-form">
            <div className="join-cards">
              {meta.joinable_ases.map((n) => (
                <button key={n} className={as === n ? "join-card sel" : "join-card"} onClick={() => setAs(n)}>
                  AS 1-{n}
                </button>
              ))}
            </div>
            <input placeholder="booth code" value={code} onChange={(e) => setCode(e.target.value)} />
            <button disabled={busy || as === null || !code} onClick={doClaim}>Get my conf</button>
            {claimErr && <p className="join-err">{claimErr}</p>}
          </div>
        )}
        {claim && <ClaimView claim={claim} v4={v4} setV4={setV4} />}
      </section>

      <Instructions />
    </div>
  );
}

function ClaimView({ claim, v4, setV4 }: { claim: ClaimResult; v4: boolean; setV4: (b: boolean) => void }) {
  const canvas = useRef<HTMLCanvasElement>(null);
  const conf = pickConf(claim, v4);
  useEffect(() => {
    if (canvas.current) QRCode.toCanvas(canvas.current, conf, { width: 220 });
  }, [conf]);
  const dl = () => {
    const a = document.createElement("a");
    a.href = URL.createObjectURL(new Blob([conf], { type: "text/plain" }));
    a.download = confFilename(claim.as);
    a.click();
  };
  return (
    <div className="join-claim">
      <p>Your endhost in <b>{claim.isd_as}</b>: <code>{claim.ip}</code></p>
      <p>scitra identity: <code>{claim.fc00_identity}</code></p>
      <canvas ref={canvas} />
      <div className="join-actions">
        <button onClick={dl}>Download {confFilename(claim.as)}</button>
        <a href={`/api/join/bundle/${claim.as}`}>Download SCION bundle (AS {claim.as})</a>
        {claim.conf_v4 && (
          <label><input type="checkbox" checked={v4} onChange={(e) => setV4(e.target.checked)} /> IPv4 endpoint</label>
        )}
      </div>
      <p className="join-note">Switching AS later? Re-download the other AS's bundle — your conf stays the same, your scitra prefix changes.</p>
    </div>
  );
}

function Instructions() {
  const [list, setList] = useState<{ name: string; title: string }[]>([]);
  const [open, setOpen] = useState<string | null>(null);
  const [html, setHtml] = useState("");
  useEffect(() => {
    fetchInstructions().then(setList).catch(() => setList([]));
  }, []);
  useEffect(() => {
    if (!open) return;
    fetchInstruction(open).then((md) => setHtml(marked.parse(md) as string)).catch(() => setHtml("<p>unavailable</p>"));
  }, [open]);
  return (
    <section className="join-tier">
      <h2>Instructions</h2>
      <div className="join-cards">
        {list.map((e) => (
          <button key={e.name} className={open === e.name ? "join-card sel" : "join-card"} onClick={() => setOpen(e.name)}>
            {e.title}
          </button>
        ))}
      </div>
      {open && <article className="join-md" dangerouslySetInnerHTML={{ __html: html }} />}
    </section>
  );
}
