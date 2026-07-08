import DOMPurify from "dompurify";
import { marked } from "marked";
import QRCode from "qrcode";
import { useEffect, useRef, useState } from "react";
import { claimConf, fetchInstruction, fetchInstructions, fetchJoinMeta, type JoinableInfo, type JoinMeta } from "../api";
import { asRole, confFilename, loadClaim, pickConf, saveClaim, type ClaimResult } from "../join";
import "../join.css";

export default function JoinPage() {
  const [meta, setMeta] = useState<JoinMeta | null>(null);
  const [metaErr, setMetaErr] = useState<string | null>(null);
  const [claim, setClaim] = useState<ClaimResult | null>(loadClaim());
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [claimErr, setClaimErr] = useState<string | null>(null);
  const [v4, setV4] = useState(false);

  useEffect(() => {
    fetchJoinMeta().then(setMeta).catch((e) => setMetaErr(String(e)));
  }, []);

  async function doClaim() {
    setBusy(true);
    setClaimErr(null);
    try {
      const c = await claimConf(code);
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
        <a href="/">← testbed map</a>
      </header>

      <section className="join-tier">
        <h2>Try it now — browser playground</h2>
        <p>A live SCION endhost shell, zero install. Booth code required.</p>
        <div className="join-cards">
          {(meta?.playground_ases ?? [152, 155, 158, 161]).map((n) => (
            <a key={n} className="join-card" href={`/play/${n}/`}>AS 1-{n} terminal</a>
          ))}
        </div>
      </section>

      <section className="join-tier">
        <h2>Join with your laptop — WireGuard</h2>
        {metaErr && <p className="join-err">Join service unreachable.</p>}
        {meta && !meta.hub_ok && <p className="join-err">Hub offline — ask at the booth.</p>}
        {meta && (
          // burned slots overlap claimed ones (the revoke flow claims THEN
          // burns a slot), so total - claimed - burned double-subtracts the
          // overlap and can go negative; clamp at 0 until the backend exposes
          // a real slots_free.
          <p className="join-slots">{Math.max(0, meta.slots_total - meta.slots_claimed - meta.slots_burned)} of {meta.slots_total} confs free</p>
        )}
        {!claim && meta && (
          <div className="join-form">
            <input placeholder="booth code" value={code} onChange={(e) => setCode(e.target.value)} />
            <button disabled={busy || !code} onClick={doClaim}>Get my conf</button>
            {claimErr && <p className="join-err">{claimErr}</p>}
          </div>
        )}
        {claim && <ClaimView claim={claim} meta={meta} v4={v4} setV4={setV4} />}
      </section>

      <Instructions />
    </div>
  );
}

function ClaimView({ claim, meta, v4, setV4 }: { claim: ClaimResult; meta: JoinMeta | null; v4: boolean; setV4: (b: boolean) => void }) {
  const canvas = useRef<HTMLCanvasElement>(null);
  const conf = pickConf(claim, v4);
  // Tabs need meta, but the conf/QR/download come from `claim` alone. When
  // meta is absent (offline, or /api/join/meta still loading for a returning
  // attendee whose claim was restored from localStorage), fall back to a
  // single tab for the attendee's own claimed AS so their conf still shows.
  const joinable: JoinableInfo[] = meta?.joinable ?? meta?.joinable_ases.map((n) => ({
    as: n, isd_as: `1-${n}`, bundle_url: `/api/join/bundle/${n}`, bootstrap_url: "",
  })) ?? [{ as: claim.as, isd_as: claim.isd_as, bundle_url: `/api/join/bundle/${claim.as}`, bootstrap_url: "" }];
  const [tab, setTab] = useState(joinable[0]?.as ?? claim.as);
  useEffect(() => {
    if (canvas.current) QRCode.toCanvas(canvas.current, conf, { width: 220 });
  }, [conf]);
  const dl = () => {
    const a = document.createElement("a");
    a.href = URL.createObjectURL(new Blob([conf], { type: "text/plain" }));
    a.download = confFilename(claim.as);
    a.click();
  };
  const cur = joinable.find((j) => j.as === tab) ?? joinable[0];
  const fc = claim.fc00_identities?.[String(tab)] ?? claim.fc00_identity;
  return (
    <div className="join-claim">
      <p>Your tunnel endpoint: <code>{claim.ip}</code></p>
      <canvas ref={canvas} />
      <div className="join-actions">
        <button onClick={dl}>Download {confFilename(claim.as)}</button>
        {claim.conf_v4 && (
          <label><input type="checkbox" checked={v4} onChange={(e) => setV4(e.target.checked)} /> IPv4 endpoint</label>
        )}
      </div>
      <p className="join-note">One conf tunnels the whole testbed. Pick an AS below to be an endhost in it — you can set up in several.</p>
      <div className="join-tabs" role="tablist">
        {joinable.map((j) => (
          <button key={j.as} role="tab" aria-selected={j.as === tab}
            className={j.as === tab ? "join-tab sel" : "join-tab"} onClick={() => setTab(j.as)}>
            AS 1-{j.as} <span className="join-tab-role">{asRole(j.as)}</span>
          </button>
        ))}
      </div>
      {cur && (
        <div className="join-tabpanel">
          <p>scitra identity in <b>{cur.isd_as}</b>: <code>{fc}</code></p>
          <div className="join-actions">
            <a href={cur.bundle_url}>Download SCION bundle (AS {cur.as})</a>
            {cur.bootstrap_url && <a href={cur.bootstrap_url} target="_blank" rel="noopener">Bootstrap URL ({cur.bootstrap_url})</a>}
          </div>
        </div>
      )}
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
    // Sanitize: the venue network serves this page over plain HTTP, so the
    // instruction markdown could be MITM-tampered in transit even though it
    // is operator-authored at rest.
    fetchInstruction(open).then((md) => setHtml(DOMPurify.sanitize(marked.parse(md) as string))).catch(() => setHtml("<p>unavailable</p>"));
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
