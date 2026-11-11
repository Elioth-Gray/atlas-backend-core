# Product Requirements Document (PRD): ATLAS Backend Core (Go & PostgreSQL)

## 1. Konteks & Tujuan Dokumen
Dokumen ini adalah spesifikasi teknis lengkap bagi *AI Coding Agent* untuk membangun **Backend Core ATLAS** menggunakan bahasa **Go (Golang)** dan **PostgreSQL (+ pgvector)**.

**Peran Arsitektural Go Backend Core:**
- **Single Source of Truth untuk State & Persistence:** Seluruh pembacaan dan penulisan ke database PostgreSQL dilakukan oleh Go (atau dikoordinasikan oleh Go). Layanan Python gRPC bersifat *stateless* terhadap state sesi.
- **Gerbang Otorisasi Multi-Tenant:** Memastikan isolasi data antar-toko (`shop_id`), termasuk memvalidasi kepemilikan `Issues` yang tidak memiliki kolom `shop_id` langsung di tabelnya.
- **Orkestrator Dua Jalur Pemrosesan (Hybrid AI Pipeline):**
  - **Jalur A (Async Worker):** Menerima feedback pelanggan dari QR, menyimpan ke PostgreSQL, dan menjadwalkan pemrosesan NLP/ML ke Python gRPC (`ProcessFeedback`), lalu menyimpan opini, vektor embedding, dan isu ke DB.
  - **Jalur B (Sync/Unary Guided Consultation):** Menyusun *SQL Evidence Snapshot*, mengelola state sesi (`COLLECTING_CONTEXT` $\rightarrow$ `SOLUTION_READY` $\rightarrow$ `ARCHIVED`), memanggil Python gRPC (`ProcessConsultationTurn`), memvalidasi *grounding/references* hasil AI, serta menyimpan hasil konsultasi dan *Business Memory* tanpa memanggil LLM lagi saat pengarsipan.

---

## 2. Skema Database & Aturan Integritas Data (DBML Reference)

### 2.1. Daftar Enum & Tabel Eksisting (Jalur A & Master Data)
- **Enums:**
  - `shop_role_enum`: `'Owner'`, `'Employee'`
  - `sentiment_enum`: `'Negative'`, `'Positive'`, `'Neutral'`
  - `severity_enum`: `'Severe'` *(Catatan Penting: Di DBML saat ini hanya terdefinisi `'Severe'`. Jika pipeline ML mengirimkan tingkat keparahan seperti Rendah/Sedang/Tinggi/Kritis atau Non-Severe, pastikan migrasi enum atau pemetaan nilai di Go disinkronkan secara aman).*
- **Tabel Master & Auth:**
  - `Users` (`id`, `name`, `email`, `phone_number`, `password`, `password_reset_token`, `refresh_token`, `created_at`, `updated_at`, `is_deleted`)
  - `Shop_Category` (`id`, `category`, `created_at`, `updated_at`, `is_deleted`)
  - `Shop_Scale` (`id`, `scale`, `created_at`, `updated_at`, `is_deleted`)
  - `Shops` (`id`, `name`, `description`, `website`, `image`, `shop_category_id`, `shop_scale_id`, `created_at`, `updated_at`, `is_deleted`)
  - `Shops_Users` (`id`, `role`, `user_id`, `shop_id`, `created_at`, `updated_at`, `is_deleted`)
- **Tabel Feedback & Klaster Isu:**
  - `Feedbacks` (`id`, `name`, `rating` decimal, `feedback` text, `image`, `shop_id`, `created_at`, `updated_at`, `is_deleted`)
  - `Issues` (`id`, `title`, `created_at`, `updated_at`, `is_deleted`)
    - **PERHATIAN KRITIS:** Kolom `status` dan `solution` **telah dihapus** dari tabel `Issues`. Tidak ada kolom `shop_id` di tabel `Issues`.
  - `Feedback_Opinions` (`id`, `opinion_text`, `evidence_text`, `sentiment`, `severity`, `feedback_id`, `issue_id`, `created_at`, `updated_at`, `is_deleted`)
  - `Opinion_Vectors` (`id`, `embedding` text/vector, `embedding_model`, `feedback_opinion_id`, `created_at`, `updated_at`, `is_deleted`)

### 2.2. Tabel Baru Modul Konsultasi AI (3 Tabel, 8 Relasi FK)
Gunakan migrasi SQL berikut secara presisi untuk ketiga tabel baru:

1. **`Consultation_Sessions`**
   - `id` UUID PK NOT NULL
   - `shop_id` UUID NOT NULL REFERENCES `Shops(id)`
   - `issue_id` UUID NOT NULL REFERENCES `Issues(id)`
   - `started_by_user_id` UUID NOT NULL REFERENCES `Users(id)`
   - `status` VARCHAR(25) NOT NULL DEFAULT `'COLLECTING_CONTEXT'` *(Nilai valid: `'COLLECTING_CONTEXT'`, `'SOLUTION_READY'`, `'ARCHIVED'`)*
   - `state_version` BIGINT NOT NULL DEFAULT `0` *(Optimistic locking untuk kontrol request bersamaan)*
   - `question_count` SMALLINT NOT NULL DEFAULT `0`
   - `asked_topics` TEXT[] NOT NULL DEFAULT `'{}'`
   - `skipped_topics` TEXT[] NOT NULL DEFAULT `'{}'`
   - `context_snapshot` JSONB NOT NULL *(Menyimpan snapshot statis saat sesi dimulai: detail issue, periode, statistik SQL, daftar evidence opini terpilih beserta ID-nya, profil shop, dan business memory aktif)*
   - `processing_request_id` UUID NULL *(Menandai request gRPC yang sedang berjalan)*
   - `final_solution_message_id` UUID NULL REFERENCES `Consultation_Messages(id)` *(Diisi saat sesi diarsipkan)*
   - `created_at` TIMESTAMPTZ NOT NULL DEFAULT `now()`
   - `updated_at` TIMESTAMPTZ NOT NULL DEFAULT `now()`
   - `archived_at` TIMESTAMPTZ NULL

2. **`Consultation_Messages`**
   - `id` UUID PK NOT NULL
   - `session_id` UUID NOT NULL REFERENCES `Consultation_Sessions(id)`
   - `role` VARCHAR(15) NOT NULL *(Nilai valid: `'OWNER'`, `'ATLAS_AI'`)*
   - `message_type` VARCHAR(20) NOT NULL *(Nilai valid: `'TEXT'`, `'SOLUTION'`)*
   - `content` TEXT NULL *(Diisi jika `message_type = 'TEXT'`)*
   - `solution_data` JSONB NULL *(Diisi jika `message_type = 'SOLUTION'`; memuat 5 komponen solusi)*
   - `revision` INTEGER NULL *(Versi solusi ke-1, 2, dst. dalam satu sesi untuk `message_type = 'SOLUTION'`)*
   - `topic_key` VARCHAR(100) NULL *(Melacak topik pertanyaan AI)*
   - `model_id` VARCHAR(100) NULL *(Nama model Gemini yang digunakan)*
   - `prompt_version` VARCHAR(50) NULL *(Versi prompt yang menghasilkan respons)*
   - `memory_candidates` JSONB NOT NULL DEFAULT `'[]'` *(Kandidat fakta bisnis yang diekstrak pada giliran tersebut)*
   - `request_id` UUID NULL *(Untuk idempotensi giliran)*
   - `created_at` TIMESTAMPTZ NOT NULL DEFAULT `now()`
   - **Index Wajib:** `CREATE INDEX idx_consultation_messages_session_order ON Consultation_Messages (session_id, created_at, id);`

3. **`Business_Memories`**
   - `id` UUID PK NOT NULL
   - `shop_id` UUID NOT NULL REFERENCES `Shops(id)`
   - `fact_text` TEXT NOT NULL
   - `source_session_id` UUID NOT NULL REFERENCES `Consultation_Sessions(id)`
   - `source_owner_message_ids` JSONB NOT NULL *(Array UUID dari `Consultation_Messages.id` dengan `role = 'OWNER'`)*
   - `confirmed_by_user_id` UUID NOT NULL REFERENCES `Users(id)`
   - `confirmed_at` TIMESTAMPTZ NOT NULL
   - `is_active` BOOLEAN NOT NULL DEFAULT `true`
   - `created_at` TIMESTAMPTZ NOT NULL DEFAULT `now()`
   - `updated_at` TIMESTAMPTZ NOT NULL DEFAULT `now()`
   - **Index Wajib:** `CREATE INDEX idx_business_memories_shop_active ON Business_Memories (shop_id, is_active);`

---

## 3. Aturan Otorisasi & Tenant Isolation (SANGAT PENTING)

1. **Validasi Akses User ke Shop:**
   Setiap endpoint `/api/owner/*` wajib memverifikasi bahwa `user_id` dari JWT token terdaftar di tabel `Shops_Users` untuk `shop_id` yang diminta dengan `is_deleted = false`.
2. **Validasi Kepemilikan `issue_id` (Tanpa `shop_id` di Tabel `Issues`):**
   Karena tabel `Issues` tidak memiliki kolom `shop_id`, setiap kali user mengakses `issue_id`, Go **WAJIB** memvalidasi kepemilikan melalui relasi join:
   ```sql
   SELECT EXISTS (
     SELECT 1
     FROM Feedback_Opinions fo
     JOIN Feedbacks f ON f.id = fo.feedback_id
     WHERE fo.issue_id = $1
       AND f.shop_id = $2
       AND fo.is_deleted = false
       AND f.is_deleted = false
   ) AS is_valid_issue_for_shop;
   ```
   Jika `false`, kembalikan HTTP `403 Forbidden` atau `404 Not Found`.

---

## 4. Spesifikasi Alur Kerja Backend (Jalur A & Jalur B)

### 4.1. Jalur A: Penerimaan Feedback & Penjadwalan NLP/ML
Mengacu pada *Sequence Diagram A (Slide 22)*:
1. **Endpoint Publik (`POST /api/public/shops/:shopId/feedbacks`):**
   - Validasi input (`rating` 1.0–5.0, `feedback` tidak kosong, `shop_id` valid dan aktif).
   - Simpan baris baru ke tabel `Feedbacks`.
   - Kembalikan respons cepat HTTP `201 Created` beserta `feedback_id` ke pelanggan (jangan menahan HTTP request pelanggan untuk menunggu proses ML selesai).
2. **Background Worker (Go Worker):**
   - Jadwalkan job asinkron untuk `feedback_id` baru.
   - Ambil riwayat kandidat klaster/vektor opini dari toko yang sama (`shop_id`) bila dibutuhkan oleh layanan ML, lalu panggil unary gRPC `ProcessFeedback` ke layanan Python.
   - Setelah menerima respons dari Python (daftar aspek/opini, sentimen, severity, embedding vektor, dan penugasan `issue_id` baru atau eksisting):
     - Lakukan transaksi database (`BEGIN ... COMMIT`) secara *idempotent* untuk melakukan `INSERT/UPSERT` ke tabel `Issues`, `Feedback_Opinions`, dan `Opinion_Vectors`.
   - **Retry & Kegagalan:** Jika panggilan gRPC ke Python gagal atau *timeout*, feedback pelanggan tetap aman di tabel `Feedbacks` dan job dijadwalkan ulang dengan *exponential backoff*.

---

### 4.2. Jalur B: Guided AI Consultation & SQL Evidence Retrieval

#### Tahap 1: Pembuatan Sesi & SQL Evidence Snapshot (`POST /api/owner/issues/:issueId/consultations`)
Saat owner memulai sesi konsultasi pada suatu planet/isu:
1. Verifikasi otorisasi `user_id` $\rightarrow$ `shop_id` $\rightarrow$ `issue_id`.
2. **Ambil Data Bukti SQL (Evidence Rules - Slide 10):**
   - **PENTING:** Hitung jumlah **Feedback Unik** (`COUNT(DISTINCT f.id)`) secara terpisah dari jumlah **Opini** (`COUNT(fo.id)`), karena satu ulasan pelanggan dapat menghasilkan lebih dari satu opini.
   - Ambil statistik agregat isu (rentang tanggal ulasan pertama & terakhir, distribusi sentimen, distribusi severity, rata-rata rating dari feedback terkait).
   - Ambil sampel opini representatif beserta teks kutipan (`fo.id AS opinion_id`, `fo.opinion_text`, `fo.evidence_text`, `fo.sentiment`, `fo.severity`, `f.feedback AS full_feedback`, `f.rating`, `f.created_at`).
   - Ambil profil toko (`Shops.name`, `Shops.description`, `Shop_Category.category`, `Shop_Scale.scale`).
   - Ambil seluruh *Business Memories* yang aktif untuk toko tersebut (`SELECT id, fact_text FROM Business_Memories WHERE shop_id = $1 AND is_active = true`).
   - Ambil ringkasan solusi terakhir (`prior_issue_solution`) jika isu ini pernah memiliki sesi `ARCHIVED` sebelumnya.
3. **Bentuk `context_snapshot` (JSONB):**
   Simpan seluruh data pada poin 2 ke kolom `Consultation_Sessions.context_snapshot` dengan `status = 'COLLECTING_CONTEXT'`, `state_version = 1`, dan `processing_request_id = request_id`.
4. **Panggil gRPC `ProcessConsultationTurn` (`event = START`):**
   - Kirim snapshot ke Python gRPC untuk mendapatkan pertanyaan pembuka pertama (`NextQuestion`) atau langsung solusi jika bukti sudah sangat lengkap.
   - Simpan respons ke `Consultation_Messages`, perbarui `question_count`, `asked_topics`, kosongkan `processing_request_id = NULL`, dan kembalikan state sesi ke Frontend.

#### Tahap 2: Pemrosesan Giliran Percakapan (`POST /api/owner/consultations/:sessionId/turns`)
Mendukung 4 jenis event dari owner:
- `OWNER_REPLY`: Owner menjawab pertanyaan AI.
- `SKIP_QUESTION`: Owner melewati pertanyaan saat ini.
- `GENERATE_NOW`: Owner meminta AI langsung membuat solusi sekarang.
- `CONTINUE_DISCUSSION`: Owner meminta revisi/diskusi lanjutan setelah berada di state `SOLUTION_READY`.

**Langkah Eksekusi & Concurrency Control di Go:**
1. **Idempotency Check:** Periksa apakah `request_id` sudah ada di `Consultation_Messages` pada `session_id` ini. Jika sudah ada, langsung kembalikan respons yang tersimpan tanpa memanggil Python lagi.
2. **Optimistic Locking (`state_version` & `processing_request_id`):**
   - Pastikan `session.status != 'ARCHIVED'`.
   - Pastikan `session.processing_request_id IS NULL`. Jika tidak null, tolak dengan HTTP `409 Conflict` (*"Sesi sedang memproses permintaan sebelumnya"*).
   - Pastikan `request.state_version == session.state_version`.
   - Lakukan atomic update untuk mengunci sesi:
     ```sql
     UPDATE Consultation_Sessions
     SET processing_request_id = $1, updated_at = now()
     WHERE id = $2 AND state_version = $3 AND processing_request_id IS NULL;
     ```
3. **Simpan Pesan Owner (Jika `OWNER_REPLY` atau `CONTINUE_DISCUSSION`):**
   - Insert baris ke `Consultation_Messages` dengan `role = 'OWNER'`, `message_type = 'TEXT'`, `content = req.Content`. Simpan UUID-nya sebagai `owner_message_id`.
4. **Panggil Unary gRPC `ProcessConsultationTurn` ke Python:**
   - Sertakan `context_snapshot`, daftar riwayat `Consultation_Messages` (beserta `id` masing-masing), `asked_topics`, `skipped_topics`, `previous_solution` (jika ada di sesi aktif), dan `event`.
5. **Validasi Respons AI di Go (Claim-Level Reference & Schema Check - Slide 12 & 23):**
   - Sebelum menyimpan ke DB, Go memvalidasi bahwa:
     - Jika hasil adalah `NextQuestion`: `topic_key` dan `text` tidak kosong.
     - Jika hasil adalah `SolutionResult`:
       - JSON memenuhi skema 5 komponen (`findings`, `customer_evidence`, `hypotheses`, `recommendations`, `success_indicators`).
       - Seluruh ID referensi yang dikutip di dalam solusi (`opinion_id`, `owner_message_id`, `memory_id`) benar-benar ada di dalam `context_snapshot` atau riwayat pesan sesi milik `shop_id` tersebut (**mencegah kebocoran lintas-tenant atau ID palsu hasil halusinasi**).
       - Semua `source_owner_message_ids` pada `memory_candidates` merujuk pada pesan dengan `role = 'OWNER'` di sesi ini.
6. **Persistensi Hasil & Transisi State:**
   - Jika `NextQuestion`:
     - Insert ke `Consultation_Messages` (`role = 'ATLAS_AI'`, `message_type = 'TEXT'`, `content`, `topic_key`, `memory_candidates`, `model_id`, `prompt_version`, `request_id`).
     - Update `Consultation_Sessions`: `status = 'COLLECTING_CONTEXT'`, `question_count = question_count + 1`, tambahkan `topic_key` ke `asked_topics` (dan `skipped_topics` jika event sebelumnya `SKIP_QUESTION`), `state_version = state_version + 1`, `processing_request_id = NULL`.
   - Jika `SolutionResult`:
     - Hitung nomor `revision` (`COALESCE(MAX(revision), 0) + 1` pada sesi tersebut).
     - Insert ke `Consultation_Messages` (`role = 'ATLAS_AI'`, `message_type = 'SOLUTION'`, `solution_data`, `revision`, `memory_candidates`, `model_id`, `prompt_version`, `request_id`).
     - Update `Consultation_Sessions`: `status = 'SOLUTION_READY'`, `state_version = state_version + 1`, `processing_request_id = NULL`.

#### Tahap 3: Simpan Hasil & Konfirmasi Business Memory (`POST /api/owner/consultations/:sessionId/save`)
**PENTING (Slide 18 & 24):** Endpoint ini adalah operasi murni Go + PostgreSQL dan **TIDAK memanggil Gemini/Python**.
1. Pastikan sesi berstatus `SOLUTION_READY` dan `final_solution_message_id` yang dikirim merujuk ke pesan `message_type = 'SOLUTION'` di sesi tersebut.
2. Dalam satu transaksi SQL:
   - Update `Consultation_Sessions`:
     - `status = 'ARCHIVED'`
     - `final_solution_message_id = req.FinalSolutionMessageID`
     - `archived_at = now()`
     - `state_version = state_version + 1`
   - Untuk setiap item di `req.ConfirmedMemories` (jika owner menyetujui 1 atau lebih kandidat memory):
     - Validasi bahwa setiap ID di `source_owner_message_ids` adalah milik `session_id` ini dengan `role = 'OWNER'`.
     - Insert ke tabel `Business_Memories` (`id`, `shop_id`, `fact_text`, `source_session_id = session.id`, `source_owner_message_ids`, `confirmed_by_user_id = user.id`, `confirmed_at = now()`, `is_active = true`).

---

## 5. Daftar Lengkap REST API Endpoints (Go Backend)

### 5.1. Public API
- `GET /api/public/shops/:shopId` — Ambil profil publik toko untuk halaman QR pelanggan.
- `POST /api/public/shops/:shopId/feedbacks` — Simpan feedback baru dari pelanggan & trigger async worker.

### 5.2. Auth & Shop Profile API
- `POST /api/auth/register` — Registrasi akun Owner baru.
- `POST /api/auth/login` — Login & terbitkan JWT (`access_token` & `refresh_token`).
- `POST /api/auth/refresh` — Perbarui token.
- `GET /api/owner/shops` — Daftar toko milik user (`Shops_Users`).
- `GET /api/owner/shops/:shopId` — Detail toko, kategori, dan skala.
- `PUT /api/owner/shops/:shopId` — Update profil toko (`Shops`) & info kontak pemilik.

### 5.3. Dashboard & Issues (Galaxy) API
- `GET /api/owner/shops/:shopId/dashboard` — Mengembalikan metrik agregat kartu KPI, time-series tren sentimen, distribusi ulasan mingguan, top 5 keluhan, dan daftar feedback terbaru.
- `GET /api/owner/shops/:shopId/issues` — Mengembalikan daftar isu toko beserta jumlah feedback unik, jumlah opini, breakdown sentimen/severity, dan status planet (`UNCONSULTED`, `CONSULTED`, `HAS_NEW_REVIEWS`).
- `GET /api/owner/issues/:issueId` — Mengembalikan detail lengkap 1 isu, statistik, 3–5 ulasan bukti terbaru, serta daftar riwayat `Consultation_Sessions` pada isu tersebut.

### 5.4. Guided AI Consultation & Business Memory API
- `POST /api/owner/issues/:issueId/consultations` — Buat sesi baru, susun `context_snapshot`, dan jalankan turn `START`.
- `GET /api/owner/consultations/:sessionId` — Ambil detail sesi beserta seluruh urutan `Consultation_Messages` (diurutkan dengan index `(session_id, created_at, id)`).
- `POST /api/owner/consultations/:sessionId/turns` — Jalankan giliran `OWNER_REPLY`, `SKIP_QUESTION`, `GENERATE_NOW`, atau `CONTINUE_DISCUSSION`.
- `POST /api/owner/consultations/:sessionId/save` — Arsipkan sesi (`ARCHIVED`) dan simpan *Business Memories* yang disetujui owner.
- `GET /api/owner/shops/:shopId/memories` — Daftar *Business Memories* toko (aktif/nonaktif).
- `PATCH /api/owner/memories/:memoryId` — Edit `fact_text` atau ubah status `is_active` pada *Business Memory*.

---

## 6. Kriteria Penerimaan (Acceptance Criteria) untuk Coding Agent
1. **Kepatuhan Skema DB:** Seluruh query SQL mematuhi struktur DBML terbaru (tidak melakukan query ke `Issues.shop_id`, `Issues.status`, atau `Issues.solution` karena ketiganya tidak ada/dihapus).
2. **Isolasi Tenant Ketat:** Tidak ada satu pun celah di mana user dari Toko A dapat membaca/memodifikasi `issue_id`, `session_id`, `message_id`, atau `memory_id` milik Toko B.
3. **Idempotensi & Concurrency:** Pengiriman ulang request dengan `request_id` yang sama tidak menduplikasi pesan atau memanggil LLM dua kali; pengiriman dua request berbeda secara bersamaan pada satu sesi ditolak dengan rapi menggunakan `processing_request_id` dan `state_version`.