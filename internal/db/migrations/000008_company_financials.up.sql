CREATE TABLE IF NOT EXISTS company_financials (
    id TEXT PRIMARY KEY,
    company_id TEXT NOT NULL UNIQUE REFERENCES companies(id),
    bank_name TEXT,
    account_name TEXT,
    bsb TEXT,
    account_number TEXT,
    swift_code TEXT,
    payment_provider TEXT CHECK (payment_provider IN ('stripe','airwallex','bank_transfer','other')),
    payment_provider_config JSONB DEFAULT '{}',
    invoice_prefix TEXT DEFAULT 'INV',
    invoice_counter INT DEFAULT 1,
    currency TEXT DEFAULT 'AUD',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
