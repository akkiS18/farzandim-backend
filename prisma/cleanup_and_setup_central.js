const { Client } = require('pg');
const fs = require('fs');
const path = require('path');
const bcrypt = require('bcryptjs');

const pgConfig = {
    user: 'postgres',
    password: 'postgres',
    host: 'localhost',
    port: 5432,
    database: 'farzandim',
};

async function main() {
    console.log("Connecting to 'farzandim' database for cleanup...");
    const client = new Client(pgConfig);
    
    try {
        await client.connect();
        
        // 1. Drop existing tenant tables and central tables to ensure a clean slate
        const tablesToDrop = [
            'audit_logs',
            'parent_access_codes',
            'grades',
            'class_teachers',
            'subjects',
            'student_parents',
            'students',
            'classes',
            'users',
            'roles',
            'schools',
            'super_admins',
            '_prisma_migrations'
        ];
        
        console.log("Dropping existing tables to clean up 'farzandim' database...");
        for (const table of tablesToDrop) {
            await client.query(`DROP TABLE IF EXISTS ${table} CASCADE`);
        }
        console.log("All tables dropped successfully.");
        
        // 2. Read and apply Central migrations
        const migrationPath = path.join(__dirname, '..', 'migrations', 'central', '000001_init_central.up.sql');
        console.log(`Reading SQL migration from: ${migrationPath}`);
        const sql = fs.readFileSync(migrationPath, 'utf8');
        
        await client.query(sql);
        console.log("Central DB tables (schools, super_admins) created successfully.");
        
        // 3. Hash password and seed Super Admin
        const phone = '+998901112233';
        const password = 'password123';
        console.log(`Hashing password for Super Admin: ${password}`);
        const passwordHash = await bcrypt.hash(password, 10);
        
        const insertQuery = `
            INSERT INTO super_admins (email, phone, password_hash, first_name, last_name)
            VALUES ($1, $2, $3, $4, $5)
            ON CONFLICT (phone) DO NOTHING;
        `;
        
        await client.query(insertQuery, [
            'superadmin@jurnal.uz',
            phone,
            passwordHash,
            'Super',
            'Admin'
        ]);
        
        console.log("Super Admin user seeded successfully!");
        console.log(`- Phone: ${phone}`);
        console.log(`- Password: ${password}`);
        
    } catch (err) {
        console.error("Error setting up 'farzandim' Central DB:", err);
        process.exit(1);
    } finally {
        await client.end();
    }
}

main();
