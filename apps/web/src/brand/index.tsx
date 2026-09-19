import { useId, type SVGProps } from "react";

export const PRODUCT_NAME_CN = "帧阅";
export const PRODUCT_NAME_EN = "Visto";
export const PRODUCT_NAME_FULL = `${PRODUCT_NAME_CN} ${PRODUCT_NAME_EN}`;
export const PRODUCT_FAMILY_STUDIO = `${PRODUCT_NAME_EN} Studio`;
export const INTERNAL_ENGINEERING_NAME = "Review Studio";

const VISTO_FOLD_PATH =
  "M205.05 139.65L205.05 100.05L70.05 100.05C58.673 100.05 49.45 109.273 49.45 120.65L49.45 238.05L93.85 244.65L93.85 156.65C93.85 147.261 101.461 139.65 110.85 139.65Z";
const VISTO_CHECK_PATH =
  "M482.45 61.85C482.45 61.85 319.378 230.851 236.114 371.368C235.425 372.532 233.77 372.593 232.995 371.484L144.25 244.65L93.85 244.65L235.85 455.65C262.95 415.466 285.948 380.343 304.203 352.057C347.014 285.721 368.419 252.554 395.05 206.85C411.064 179.366 426.489 151.682 454.65 106.05C466.195 87.341 475.912 72.049 482.45 61.85Z";

export function VistoMark(props: SVGProps<SVGSVGElement>) {
  const id = useId();
  const foldGradientId = `${id}-fold`;
  const checkGradientId = `${id}-check`;
  const glowId = `${id}-glow`;

  return (
    <svg viewBox="0 0 531.9 517.5" fill="none" aria-hidden="true" {...props}>
      <defs>
        <linearGradient
          id={foldGradientId}
          x1="49.45"
          y1="100.05"
          x2="205.05"
          y2="244.65"
          gradientUnits="userSpaceOnUse"
        >
          <stop offset="0" stopColor="#57F7FF" />
          <stop offset="0.5" stopColor="#00AFFF" />
          <stop offset="1" stopColor="#245CFF" />
        </linearGradient>
        <linearGradient
          id={checkGradientId}
          x1="118"
          y1="452"
          x2="482.45"
          y2="61.85"
          gradientUnits="userSpaceOnUse"
        >
          <stop offset="0" stopColor="#0B48FF" />
          <stop offset="0.52" stopColor="#008DFF" />
          <stop offset="1" stopColor="#24F7FF" />
        </linearGradient>
        <filter
          id={glowId}
          x="-35%"
          y="-35%"
          width="170%"
          height="170%"
          colorInterpolationFilters="sRGB"
        >
          <feGaussianBlur stdDeviation="12" result="blur" />
          <feColorMatrix
            in="blur"
            type="matrix"
            values="0 0 0 0 0 0 0 0 0 .58 0 0 0 0 1 0 0 0 .78 0"
          />
          <feMerge>
            <feMergeNode />
            <feMergeNode in="SourceGraphic" />
          </feMerge>
        </filter>
      </defs>
      <g filter={`url(#${glowId})`}>
        <path d={VISTO_FOLD_PATH} fill={`url(#${foldGradientId})`} />
        <path d={VISTO_CHECK_PATH} fill={`url(#${checkGradientId})`} />
        <path
          d={VISTO_CHECK_PATH}
          fill="none"
          stroke="#85DDFF"
          strokeWidth="4.4"
          opacity="0.42"
        />
      </g>
    </svg>
  );
}
