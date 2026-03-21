import { useEffect, useState } from "react";

interface Contact {
  id: number;
  name: string;
  phone: string;
}

interface Site {
  id: number;
  address: string;
  managerName: string;
  managerPhone: string;
}

const PHONE_REGEX = /^01[016789]-\d{3,4}-\d{4}$/;

function formatPhoneInput(value: string): string {
  const digits = value.replace(/\D/g, "");
  if (digits.length <= 3) return digits;
  if (digits.length <= 7) return `${digits.slice(0, 3)}-${digits.slice(3)}`;
  return `${digits.slice(0, 3)}-${digits.slice(3, 7)}-${digits.slice(7, 11)}`;
}

function getAuthHeaders(): HeadersInit {
  const token = localStorage.getItem("token");
  return token
    ? { Authorization: `Bearer ${token}`, "Content-Type": "application/json" }
    : { "Content-Type": "application/json" };
}

export default function ManagementPage() {
  const [contacts, setContacts] = useState<Contact[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Site info state
  const [sites, setSites] = useState<Site[]>([]);
  const [sitesLoading, setSitesLoading] = useState(true);
  const [sitesError, setSitesError] = useState<string | null>(null);
  const [siteEditId, setSiteEditId] = useState<number | null>(null);
  const [siteEditAddress, setSiteEditAddress] = useState("");
  const [siteEditManagerName, setSiteEditManagerName] = useState("");
  const [siteEditManagerPhone, setSiteEditManagerPhone] = useState("");
  const [siteEditError, setSiteEditError] = useState<string | null>(null);
  const [siteEditLoading, setSiteEditLoading] = useState(false);

  // Add form
  const [showAddForm, setShowAddForm] = useState(false);
  const [addName, setAddName] = useState("");
  const [addPhone, setAddPhone] = useState("");
  const [addError, setAddError] = useState<string | null>(null);
  const [addLoading, setAddLoading] = useState(false);

  // Edit state
  const [editId, setEditId] = useState<number | null>(null);
  const [editName, setEditName] = useState("");
  const [editPhone, setEditPhone] = useState("");
  const [editError, setEditError] = useState<string | null>(null);
  const [editLoading, setEditLoading] = useState(false);

  // Delete confirmation
  const [deleteTarget, setDeleteTarget] = useState<Contact | null>(null);
  const [deleteLoading, setDeleteLoading] = useState(false);

  const fetchContacts = async () => {
    try {
      const res = await fetch("/api/contacts", { headers: getAuthHeaders() });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: Contact[] = await res.json();
      setContacts(data);
      setError(null);
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "연락처를 불러올 수 없습니다"
      );
    } finally {
      setLoading(false);
    }
  };

  const fetchSites = async () => {
    try {
      const res = await fetch("/api/sites", { headers: getAuthHeaders() });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: Site[] = await res.json();
      setSites(data);
      setSitesError(null);
    } catch (err) {
      setSitesError(
        err instanceof Error ? err.message : "현장 정보를 불러올 수 없습니다"
      );
    } finally {
      setSitesLoading(false);
    }
  };

  useEffect(() => {
    fetchContacts();
    fetchSites();
  }, []);

  const startSiteEdit = (site: Site) => {
    setSiteEditId(site.id);
    setSiteEditAddress(site.address);
    setSiteEditManagerName(site.managerName);
    setSiteEditManagerPhone(site.managerPhone);
    setSiteEditError(null);
  };

  const cancelSiteEdit = () => {
    setSiteEditId(null);
    setSiteEditError(null);
  };

  const handleSiteEdit = async () => {
    if (siteEditId === null) return;
    setSiteEditError(null);
    if (!siteEditAddress.trim()) {
      setSiteEditError("주소를 입력하세요");
      return;
    }
    if (!siteEditManagerName.trim()) {
      setSiteEditError("담당자 이름을 입력하세요");
      return;
    }
    if (!PHONE_REGEX.test(siteEditManagerPhone)) {
      setSiteEditError("전화번호 형식이 올바르지 않습니다 (예: 010-1234-5678)");
      return;
    }
    setSiteEditLoading(true);
    try {
      const res = await fetch(`/api/sites/${siteEditId}`, {
        method: "PUT",
        headers: getAuthHeaders(),
        body: JSON.stringify({
          address: siteEditAddress.trim(),
          managerName: siteEditManagerName.trim(),
          managerPhone: siteEditManagerPhone,
        }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => null);
        throw new Error(body?.error || `HTTP ${res.status}`);
      }
      setSiteEditId(null);
      await fetchSites();
    } catch (err) {
      setSiteEditError(err instanceof Error ? err.message : "수정 실패");
    } finally {
      setSiteEditLoading(false);
    }
  };

  const handleAdd = async () => {
    setAddError(null);
    if (!addName.trim()) {
      setAddError("이름을 입력하세요");
      return;
    }
    if (!PHONE_REGEX.test(addPhone)) {
      setAddError("전화번호 형식이 올바르지 않습니다 (예: 010-1234-5678)");
      return;
    }
    setAddLoading(true);
    try {
      const res = await fetch("/api/contacts", {
        method: "POST",
        headers: getAuthHeaders(),
        body: JSON.stringify({ name: addName.trim(), phone: addPhone }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => null);
        throw new Error(body?.error || `HTTP ${res.status}`);
      }
      setAddName("");
      setAddPhone("");
      setShowAddForm(false);
      await fetchContacts();
    } catch (err) {
      setAddError(err instanceof Error ? err.message : "추가 실패");
    } finally {
      setAddLoading(false);
    }
  };

  const startEdit = (contact: Contact) => {
    setEditId(contact.id);
    setEditName(contact.name);
    setEditPhone(contact.phone);
    setEditError(null);
  };

  const cancelEdit = () => {
    setEditId(null);
    setEditError(null);
  };

  const handleEdit = async () => {
    if (editId === null) return;
    setEditError(null);
    if (!editName.trim()) {
      setEditError("이름을 입력하세요");
      return;
    }
    if (!PHONE_REGEX.test(editPhone)) {
      setEditError("전화번호 형식이 올바르지 않습니다");
      return;
    }
    setEditLoading(true);
    try {
      const res = await fetch(`/api/contacts/${editId}`, {
        method: "PUT",
        headers: getAuthHeaders(),
        body: JSON.stringify({ name: editName.trim(), phone: editPhone }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => null);
        throw new Error(body?.error || `HTTP ${res.status}`);
      }
      setEditId(null);
      await fetchContacts();
    } catch (err) {
      setEditError(err instanceof Error ? err.message : "수정 실패");
    } finally {
      setEditLoading(false);
    }
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    setDeleteLoading(true);
    try {
      const res = await fetch(`/api/contacts/${deleteTarget.id}`, {
        method: "DELETE",
        headers: getAuthHeaders(),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      setDeleteTarget(null);
      await fetchContacts();
    } catch {
      setDeleteTarget(null);
    } finally {
      setDeleteLoading(false);
    }
  };

  if (loading) {
    return (
      <div className="page">
        <h2>연락처 관리</h2>
        <p className="mgmt-loading">로딩 중...</p>
      </div>
    );
  }

  if (error) {
    return (
      <div className="page">
        <h2>연락처 관리</h2>
        <p className="mgmt-error">{error}</p>
      </div>
    );
  }

  return (
    <div className="page">
      {/* Site info section */}
      <div className="mgmt-header">
        <h2>현장 정보</h2>
      </div>
      {sitesLoading ? (
        <p className="mgmt-loading">로딩 중...</p>
      ) : sitesError ? (
        <p className="mgmt-error">{sitesError}</p>
      ) : sites.length === 0 ? (
        <p className="mgmt-empty">등록된 현장 정보가 없습니다</p>
      ) : (
        <div className="mgmt-list">
          {sites.map((site) =>
            siteEditId === site.id ? (
              <div key={site.id} className="mgmt-card mgmt-card-editing">
                <div className="mgmt-form-field">
                  <label>주소</label>
                  <input
                    type="text"
                    value={siteEditAddress}
                    onChange={(e) => setSiteEditAddress(e.target.value)}
                    placeholder="현장 주소"
                    autoFocus
                  />
                </div>
                <div className="mgmt-form-field">
                  <label>담당자 이름</label>
                  <input
                    type="text"
                    value={siteEditManagerName}
                    onChange={(e) => setSiteEditManagerName(e.target.value)}
                    placeholder="홍길동"
                  />
                </div>
                <div className="mgmt-form-field">
                  <label>담당자 전화번호</label>
                  <input
                    type="tel"
                    value={siteEditManagerPhone}
                    onChange={(e) =>
                      setSiteEditManagerPhone(formatPhoneInput(e.target.value))
                    }
                    placeholder="010-1234-5678"
                    maxLength={13}
                  />
                </div>
                {siteEditError && (
                  <p className="mgmt-form-error">{siteEditError}</p>
                )}
                <div className="mgmt-form-actions">
                  <button
                    className="mgmt-btn mgmt-btn-primary"
                    onClick={handleSiteEdit}
                    disabled={siteEditLoading}
                  >
                    {siteEditLoading ? "저장 중..." : "저장"}
                  </button>
                  <button
                    className="mgmt-btn mgmt-btn-secondary"
                    onClick={cancelSiteEdit}
                  >
                    취소
                  </button>
                </div>
              </div>
            ) : (
              <div key={site.id} className="mgmt-card">
                <div className="mgmt-card-info">
                  <span className="mgmt-card-name">{site.address || "주소 미등록"}</span>
                  <span className="mgmt-card-phone">
                    담당자: {site.managerName || "-"} / {site.managerPhone || "-"}
                  </span>
                </div>
                <div className="mgmt-card-actions">
                  <button
                    className="mgmt-btn mgmt-btn-small"
                    onClick={() => startSiteEdit(site)}
                  >
                    수정
                  </button>
                </div>
              </div>
            )
          )}
        </div>
      )}

      <div className="mgmt-section-divider" />

      {/* Contacts section */}
      <div className="mgmt-header">
        <h2>연락처 관리</h2>
        {!showAddForm && (
          <button
            className="mgmt-btn mgmt-btn-primary"
            onClick={() => {
              setShowAddForm(true);
              setAddError(null);
            }}
          >
            + 추가
          </button>
        )}
      </div>

      {/* Add form */}
      {showAddForm && (
        <div className="mgmt-form">
          <div className="mgmt-form-field">
            <label>이름</label>
            <input
              type="text"
              value={addName}
              onChange={(e) => setAddName(e.target.value)}
              placeholder="홍길동"
              autoFocus
            />
          </div>
          <div className="mgmt-form-field">
            <label>전화번호</label>
            <input
              type="tel"
              value={addPhone}
              onChange={(e) => setAddPhone(formatPhoneInput(e.target.value))}
              placeholder="010-1234-5678"
              maxLength={13}
            />
          </div>
          {addError && <p className="mgmt-form-error">{addError}</p>}
          <div className="mgmt-form-actions">
            <button
              className="mgmt-btn mgmt-btn-primary"
              onClick={handleAdd}
              disabled={addLoading}
            >
              {addLoading ? "저장 중..." : "저장"}
            </button>
            <button
              className="mgmt-btn mgmt-btn-secondary"
              onClick={() => {
                setShowAddForm(false);
                setAddName("");
                setAddPhone("");
                setAddError(null);
              }}
            >
              취소
            </button>
          </div>
        </div>
      )}

      {/* Contact list */}
      {contacts.length === 0 ? (
        <p className="mgmt-empty">등록된 연락처가 없습니다</p>
      ) : (
        <div className="mgmt-list">
          {contacts.map((contact) =>
            editId === contact.id ? (
              <div key={contact.id} className="mgmt-card mgmt-card-editing">
                <div className="mgmt-form-field">
                  <label>이름</label>
                  <input
                    type="text"
                    value={editName}
                    onChange={(e) => setEditName(e.target.value)}
                    autoFocus
                  />
                </div>
                <div className="mgmt-form-field">
                  <label>전화번호</label>
                  <input
                    type="tel"
                    value={editPhone}
                    onChange={(e) =>
                      setEditPhone(formatPhoneInput(e.target.value))
                    }
                    maxLength={13}
                  />
                </div>
                {editError && <p className="mgmt-form-error">{editError}</p>}
                <div className="mgmt-form-actions">
                  <button
                    className="mgmt-btn mgmt-btn-primary"
                    onClick={handleEdit}
                    disabled={editLoading}
                  >
                    {editLoading ? "저장 중..." : "저장"}
                  </button>
                  <button
                    className="mgmt-btn mgmt-btn-secondary"
                    onClick={cancelEdit}
                  >
                    취소
                  </button>
                </div>
              </div>
            ) : (
              <div key={contact.id} className="mgmt-card">
                <div className="mgmt-card-info">
                  <span className="mgmt-card-name">{contact.name}</span>
                  <span className="mgmt-card-phone">{contact.phone}</span>
                </div>
                <div className="mgmt-card-actions">
                  <button
                    className="mgmt-btn mgmt-btn-small"
                    onClick={() => startEdit(contact)}
                  >
                    수정
                  </button>
                  <button
                    className="mgmt-btn mgmt-btn-small mgmt-btn-danger"
                    onClick={() => setDeleteTarget(contact)}
                  >
                    삭제
                  </button>
                </div>
              </div>
            )
          )}
        </div>
      )}

      {/* Delete confirmation dialog */}
      {deleteTarget && (
        <div className="mgmt-modal-overlay" onClick={() => setDeleteTarget(null)}>
          <div className="mgmt-modal" onClick={(e) => e.stopPropagation()}>
            <p className="mgmt-modal-text">
              <strong>{deleteTarget.name}</strong> 연락처를 삭제하시겠습니까?
            </p>
            <div className="mgmt-form-actions">
              <button
                className="mgmt-btn mgmt-btn-danger"
                onClick={handleDelete}
                disabled={deleteLoading}
              >
                {deleteLoading ? "삭제 중..." : "삭제"}
              </button>
              <button
                className="mgmt-btn mgmt-btn-secondary"
                onClick={() => setDeleteTarget(null)}
              >
                취소
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
