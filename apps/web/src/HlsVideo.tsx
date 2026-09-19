import type Hls from "hls.js";
import {
  forwardRef,
  useCallback,
  useEffect,
  useRef,
  type MutableRefObject,
  type VideoHTMLAttributes,
} from "react";

type HlsVideoProps = Omit<VideoHTMLAttributes<HTMLVideoElement>, "src"> & {
  src: string;
};

type HlsModule = typeof import("hls.js");

let hlsModulePromise: Promise<HlsModule> | null = null;

function loadHlsModule(): Promise<HlsModule> {
  if (!hlsModulePromise) {
    hlsModulePromise = import("hls.js");
  }
  return hlsModulePromise;
}

export const HlsVideo = forwardRef<HTMLVideoElement, HlsVideoProps>(
  function HlsVideo({ src, ...props }, forwardedRef) {
    const elementRef = useRef<HTMLVideoElement | null>(null);
    const setRef = useCallback(
      (element: HTMLVideoElement | null) => {
        elementRef.current = element;
        if (typeof forwardedRef === "function") {
          forwardedRef(element);
        } else if (forwardedRef) {
          (forwardedRef as MutableRefObject<HTMLVideoElement | null>).current =
            element;
        }
      },
      [forwardedRef],
    );

    useEffect(() => {
      const video = elementRef.current;
      if (!video) return;

      let hls: Hls | null = null;
      let cancelled = false;

      if (isHlsSource(src)) {
        if (video.canPlayType("application/vnd.apple.mpegurl")) {
          video.src = src;
        } else {
          void loadHlsModule()
            .then(({ default: HlsConstructor }) => {
              if (cancelled) return;
              if (HlsConstructor.isSupported()) {
                hls = new HlsConstructor();
                hls.loadSource(src);
                hls.attachMedia(video);
              } else {
                video.src = src;
              }
            })
            .catch(() => {
              if (!cancelled) {
                video.src = src;
              }
            });
          return () => {
            cancelled = true;
            if (hls) {
              hls.destroy();
            }
            video.removeAttribute("src");
            video.load();
          };
        }
      } else {
        video.src = src;
      }

      return () => {
        cancelled = true;
        if (hls) {
          hls.destroy();
        }
        video.removeAttribute("src");
        video.load();
      };
    }, [src]);

    return <video ref={setRef} {...props} />;
  },
);

function isHlsSource(src: string) {
  return src.includes(".m3u8") || src.includes("/hls/");
}
