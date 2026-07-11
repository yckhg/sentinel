import { useState, useEffect } from "react";
import { fetchWithTimeout } from "../utils/fetchWithTimeout";
import Modal from "./Modal";
import { formatGps, gpsStatusText } from "./emergencyLocation";

interface EmergencyCallButtonProps {
  className?: string;
  compact?: boolean;
}

interface SiteInfo {
  id: number;
  address: string;
  managerName: string;
  managerPhone: string;
}

export default function EmergencyCallButton({
  className,
  compact,
}: EmergencyCallButtonProps) {
  const [showDialog, setShowDialog] = useState(false);
  const [siteAddress, setSiteAddress] = useState<string | null>(null);
  const [geoAddress, setGeoAddress] = useState<string | null>(null);
  const [geoLoading, setGeoLoading] = useState(false);
  const [geoDenied, setGeoDenied] = useState(false);
  // Set after a tel: attempt so we can show a fallback for desktop/browsers
  // where tel: is a no-op.
  const [callAttempted, setCallAttempted] = useState(false);

  useEffect(() => {
    if (!showDialog) return;

    // Fetch site address from settings
    const token = localStorage.getItem("token");
    if (token) {
      fetchWithTimeout("/api/sites", {
        headers: { Authorization: `Bearer ${token}` },
      })
        .then((res) => (res.ok ? res.json() : Promise.reject()))
        .then((sites: SiteInfo[]) => {
          const first = sites[0];
          if (first && first.address) {
            setSiteAddress(first.address);
          }
        })
        .catch(() => {});
    }

    // Request geolocation
    if (navigator.geolocation) {
      setGeoLoading(true);
      navigator.geolocation.getCurrentPosition(
        (pos) => {
          setGeoAddress(formatGps(pos.coords.latitude, pos.coords.longitude));
          setGeoLoading(false);
          setGeoDenied(false);
        },
        () => {
          // Surface the failure instead of silently falling back (#98).
          setGeoLoading(false);
          setGeoDenied(true);
        },
        { timeout: 5000 }
      );
    }
  }, [showDialog]);

  const handleCall = () => {
    setCallAttempted(true);
    window.location.href = "tel:119";
  };

  const handleClose = () => {
    setShowDialog(false);
    setGeoAddress(null);
    setGeoLoading(false);
    setGeoDenied(false);
    setCallAttempted(false);
  };

  const gpsText = gpsStatusText({
    coords: geoAddress,
    loading: geoLoading,
    denied: geoDenied,
  });

  return (
    <>
      <button
        className={`emergency-call-btn ${compact ? "emergency-call-btn-compact" : ""} ${className || ""}`}
        onClick={() => setShowDialog(true)}
        aria-label="119 신고"
      >
        {compact ? "119" : "119 신고"}
      </button>

      {showDialog && (
        <Modal onClose={handleClose} ariaLabel="119 긴급 신고">
            <div className="emergency-dialog-header">119 긴급 신고</div>
            {/* Always show the registered site address, and GPS alongside it —
                the registered address is what a caller can read out to 119,
                while GPS is supplementary (#98). */}
            <div className="emergency-dialog-address">
              <span className="emergency-dialog-label">등록 현장주소</span>
              <span className="emergency-dialog-value">
                {siteAddress || "등록된 현장주소가 없습니다"}
              </span>
            </div>
            <div className="emergency-dialog-address">
              <span className="emergency-dialog-label">현재 위치(GPS)</span>
              <span className="emergency-dialog-value">{gpsText}</span>
            </div>
            <p className="mgmt-modal-text">119에 전화를 걸겠습니까?</p>
            {callAttempted && (
              <p className="emergency-dialog-fallback">
                전화가 자동으로 걸리지 않으면 직접 <strong>119</strong>로 전화해 주세요.
              </p>
            )}
            <div className="mgmt-form-actions" style={{ justifyContent: "center" }}>
              <button className="mgmt-btn mgmt-btn-secondary" onClick={handleClose}>
                {callAttempted ? "닫기" : "취소"}
              </button>
              <button className="mgmt-btn mgmt-btn-danger" onClick={handleCall}>
                119 전화 걸기
              </button>
            </div>
        </Modal>
      )}
    </>
  );
}
