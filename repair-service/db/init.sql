CREATE TABLE IF NOT EXISTS repair_jobs (
    job_id SERIAL PRIMARY KEY,
    vehicle_id INTEGER NOT NULL,
    problem_description TEXT NOT NULL,
    repair_status VARCHAR(50) NOT NULL,
    cost DECIMAL(10, 2) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);