#!/usr/bin/env bash
# cctv-adapter §단언 B (상태 스키마): GET /api/cameras/status 응답이
#   (1) JSON 배열이고 원소 수 == 설정된 카메라 수 N,
#   (2) 각 원소: cameraId(string), status ∈ {connected,disconnected,reconnecting},
#       connectedAt(RFC3339|null), lastError(string|null).
# READ-ONLY. N은 현재 부트 설정 파일 기준. (현재 프로덕션 N=0 — 스키마 검사는 공허 통과)
set -u

CFG_N=$(docker exec sentinel-cctv-adapter sh -c 'cat ${CAMERAS_CONFIG_PATH:-/config/cameras.json} 2>/dev/null' | jq 'length' 2>/dev/null || echo "?")
BODY=$(docker exec sentinel-cctv-adapter wget -qO- http://localhost:8080/api/cameras/status)
echo "config N=$CFG_N"
echo "body: $BODY"

printf '%s' "$BODY" | jq -e 'type == "array"' >/dev/null || { echo "NOK: not a JSON array"; exit 1; }
LEN=$(printf '%s' "$BODY" | jq 'length')
[ "$LEN" = "$CFG_N" ] || { echo "NOK: array length $LEN != configured cameras $CFG_N"; exit 1; }
printf '%s' "$BODY" | jq -e 'all(.[];
    (.cameraId | type == "string") and
    (.status | IN("connected","disconnected","reconnecting")) and
    ((.connectedAt == null) or (.connectedAt | test("^\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2}(\\.\\d+)?(Z|[+-]\\d{2}:\\d{2})$"))) and
    ((.lastError == null) or (.lastError | type == "string")))' >/dev/null \
  || { echo "NOK: element schema violation"; exit 1; }
echo "OK (N=$LEN)"
exit 0
