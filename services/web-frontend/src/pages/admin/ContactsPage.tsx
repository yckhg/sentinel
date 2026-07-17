import { useEffect, useState } from "react";
import { navigate } from "../../utils/navigation";
import {
  fetchWithTimeout,
  isTimeoutError,
  timeoutMessage,
} from "../../utils/fetchWithTimeout";
import { PHONE_REGEX, formatPhoneInput } from "../../utils/phone";
import Modal from "../../components/Modal";

interface Contact {
  id: number;
  name: string;
  phone: string;
  email: string;
  notifyEmail: boolean;
}

function getAuthHeaders(): HeadersInit {
  const token = localStorage.getItem("token");
  return token
    ? { Authorization: `Bearer ${token}`, "Content-Type": "application/json" }
    : { "Content-Type": "application/json" };
}

export default function ContactsPage() {
  const [contacts, setContacts] = useState<Contact[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Add form
  const [showAddForm, setShowAddForm] = useState(false);
  const [addName, setAddName] = useState("");
  const [addPhone, setAddPhone] = useState("");
  const [addEmail, setAddEmail] = useState("");
  const [addNotifyEmail, setAddNotifyEmail] = useState(false);
  const [addError, setAddError] = useState<string | null>(null);
  const [addLoading, setAddLoading] = useState(false);

  // Edit state
  const [editId, setEditId] = useState<number | null>(null);
  const [editName, setEditName] = useState("");
  const [editPhone, setEditPhone] = useState("");
  const [editEmail, setEditEmail] = useState("");
  const [editNotifyEmail, setEditNotifyEmail] = useState(false);
  const [editError, setEditError] = useState<string | null>(null);
  const [editLoading, setEditLoading] = useState(false);

  // Delete confirmation
  const [deleteTarget, setDeleteTarget] = useState<Contact | null>(null);
  const [deleteLoading, setDeleteLoading] = useState(false);

  // Shared inline error for the confirm/delete modal. Previously delete
  // failures were silently swallowed (modal just closed) so the user assumed
  // success (#103).
  const [actionError, setActionError] = useState<string | null>(null);

  const errorMessage = (err: unknown): string =>
    isTimeoutError(err)
      ? timeoutMessage()
      : err instanceof Error
        ? err.message
        : "요청을 처리하지 못했습니다";

  const fetchContacts = async () => {
    try {
      const res = await fetchWithTimeout("/api/contacts", { headers: getAuthHeaders() });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: Contact[] = await res.json();
      setContacts(data);
      setError(null);
    } catch (err) {
      setError(
        isTimeoutError(err)
          ? timeoutMessage()
          : err instanceof Error ? err.message : "연락처를 불러올 수 없습니다"
      );
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchContacts();
  }, []);

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
      const res = await fetchWithTimeout("/api/contacts", {
        method: "POST",
        headers: getAuthHeaders(),
        body: JSON.stringify({ name: addName.trim(), phone: addPhone, email: addEmail.trim(), notifyEmail: addNotifyEmail }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => null);
        throw new Error(body?.error || `HTTP ${res.status}`);
      }
      setAddName("");
      setAddPhone("");
      setAddEmail("");
      setAddNotifyEmail(false);
      setShowAddForm(false);
      await fetchContacts();
    } catch (err) {
      setAddError(isTimeoutError(err) ? timeoutMessage() : err instanceof Error ? err.message : "추가 실패");
    } finally {
      setAddLoading(false);
    }
  };

  const startEdit = (contact: Contact) => {
    setEditId(contact.id);
    setEditName(contact.name);
    setEditPhone(contact.phone);
    setEditEmail(contact.email || "");
    setEditNotifyEmail(contact.notifyEmail);
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
      const res = await fetchWithTimeout(`/api/contacts/${editId}`, {
        method: "PUT",
        headers: getAuthHeaders(),
        body: JSON.stringify({ name: editName.trim(), phone: editPhone, email: editEmail.trim(), notifyEmail: editNotifyEmail }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => null);
        throw new Error(body?.error || `HTTP ${res.status}`);
      }
      setEditId(null);
      await fetchContacts();
    } catch (err) {
      setEditError(isTimeoutError(err) ? timeoutMessage() : err instanceof Error ? err.message : "수정 실패");
    } finally {
      setEditLoading(false);
    }
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    setDeleteLoading(true);
    setActionError(null);
    try {
      const res = await fetchWithTimeout(`/api/contacts/${deleteTarget.id}`, {
        method: "DELETE",
        headers: getAuthHeaders(),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      setDeleteTarget(null);
      await fetchContacts();
    } catch (err) {
      setActionError(errorMessage(err)); // keep modal open, surface failure (#103)
    } finally {
      setDeleteLoading(false);
    }
  };

  return (
    <div className="admin-page page" data-testid="admin-page" data-slug="contacts">
      <button
        type="button"
        className="admin-back"
        data-testid="admin-back"
        onClick={() => navigate("/admin")}
      >
        ← 관리
      </button>

      <div className="mgmt-header">
        <h2>연락처 관리</h2>
        {!loading && !error && !showAddForm && (
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

      {loading ? (
        <p className="mgmt-loading">로딩 중...</p>
      ) : error ? (
        <p className="mgmt-error">{error}</p>
      ) : (
        <>
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
              <div className="mgmt-form-field">
                <label>이메일</label>
                <input
                  type="email"
                  value={addEmail}
                  onChange={(e) => setAddEmail(e.target.value)}
                  placeholder="example@email.com"
                />
              </div>
              <div className="mgmt-form-field mgmt-form-checkbox">
                <label>
                  <input
                    type="checkbox"
                    checked={addNotifyEmail}
                    onChange={(e) => setAddNotifyEmail(e.target.checked)}
                  />
                  이메일 알림 수신
                </label>
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
                    setAddEmail("");
                    setAddNotifyEmail(false);
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
                    <div className="mgmt-form-field">
                      <label>이메일</label>
                      <input
                        type="email"
                        value={editEmail}
                        onChange={(e) => setEditEmail(e.target.value)}
                        placeholder="example@email.com"
                      />
                    </div>
                    <div className="mgmt-form-field mgmt-form-checkbox">
                      <label>
                        <input
                          type="checkbox"
                          checked={editNotifyEmail}
                          onChange={(e) => setEditNotifyEmail(e.target.checked)}
                        />
                        이메일 알림 수신
                      </label>
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
                      {contact.email && <span className="mgmt-card-email">{contact.email}</span>}
                      {contact.notifyEmail && <span className="mgmt-card-badge">이메일 알림</span>}
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
        </>
      )}

      {/* Delete confirmation modal */}
      {deleteTarget && (
        <Modal
          onClose={() => { setDeleteTarget(null); setActionError(null); }}
          ariaLabel="연락처 삭제 확인"
        >
          <p className="mgmt-modal-text">
            <strong>{deleteTarget.name}</strong> 연락처를 삭제하시겠습니까?
          </p>
          {actionError && <p className="mgmt-form-error">{actionError}</p>}
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
              onClick={() => { setDeleteTarget(null); setActionError(null); }}
            >
              취소
            </button>
          </div>
        </Modal>
      )}
    </div>
  );
}
