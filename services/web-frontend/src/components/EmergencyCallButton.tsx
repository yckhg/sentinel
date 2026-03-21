import { useState, useEffect } from "react";

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

  useEffect(() => {
    if (!showDialog) return;

    // Fetch site address from settings
    const token = localStorage.getItem("token");
    if (token) {
      fetch("/api/sites", {
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
          setGeoAddress(
            `${pos.coords.latitude.toFixed(5)}, ${pos.coords.longitude.toFixed(5)}`
          );
          setGeoLoading(false);
        },
        () => {
          setGeoLoading(false);
        },
        { timeout: 5000 }
      );
    }
  }, [showDialog]);

  const handleCall = () => {
    setShowDialog(false);
    window.location.href = "tel:119";
  };

  const handleClose = () => {
    setShowDialog(false);
    setGeoAddress(null);
    setGeoLoading(false);
  };

  const displayAddress = geoAddress
    ? `GPS: ${geoAddress}`
    : siteAddress || "주소 정보 없음";

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
        <div className="mgmt-modal-overlay" onClick={handleClose}>
          <div className="mgmt-modal" onClick={(e) => e.stopPropagation()}>
            <div className="emergency-dialog-header">119 긴급 신고</div>
            <div className="emergency-dialog-address">
              <span className="emergency-dialog-label">현재 위치</span>
              <span className="emergency-dialog-value">
                {geoLoading ? "위치 확인 중..." : displayAddress}
              </span>
            </div>
            <p className="mgmt-modal-text">119에 전화를 걸겠습니까?</p>
            <div className="mgmt-form-actions" style={{ justifyContent: "center" }}>
              <button className="mgmt-btn mgmt-btn-secondary" onClick={handleClose}>
                취소
              </button>
              <button className="mgmt-btn mgmt-btn-danger" onClick={handleCall}>
                119 전화 걸기
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
