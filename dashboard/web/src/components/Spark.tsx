// Spark — the panel sparkline, a direct port of the mockup's spark(). Draws a
// 320×38 (attribute-size) canvas: a 1.5px polyline of the series scaled to its
// own max, with a filled endpoint dot. The 2 / 5 / 10 / 11 px paddings are the
// mockup's padding fix so the stroke and the endpoint dot never clip the canvas
// edges. Redraws whenever the data array or color changes.
import { useEffect, useRef } from "react";

interface SparkProps {
  data: number[];
  color: string;
}

export default function Spark({ data, color }: SparkProps) {
  const ref = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const cv = ref.current;
    if (!cv) return;
    const c = cv.getContext("2d");
    if (!c) return;
    const w = cv.width;
    const h = cv.height;
    c.clearRect(0, 0, w, h);
    if (data.length < 2) return;

    const max = Math.max(...data, 0.001);
    const min = 0;
    const px = (i: number) => 2 + (i / (data.length - 1)) * (w - 10);
    const py = (v: number) => h - 5 - ((v - min) / (max - min)) * (h - 11);

    c.beginPath();
    data.forEach((v, i) => (i ? c.lineTo(px(i), py(v)) : c.moveTo(px(i), py(v))));
    c.strokeStyle = color;
    c.lineWidth = 1.5;
    c.stroke();

    c.beginPath();
    c.arc(px(data.length - 1), py(data[data.length - 1]), 2.5, 0, 7);
    c.fillStyle = "#F2F4F8";
    c.fill();
  }, [data, color]);

  return <canvas ref={ref} width={320} height={38} />;
}
