package main

import (
	"log"
	"strings"

	"github.com/farzandim/backend/internal/config"
	"github.com/farzandim/backend/internal/db"
	"github.com/farzandim/backend/internal/handlers"
	"github.com/farzandim/backend/internal/middleware"
	"github.com/gin-gonic/gin"
)

func main() {
	// 1. Load configurations from environment or .env
	cfg := config.LoadConfig()

	// 2. Initialize Central DB pool and Tenant DB Connection manager
	db.InitCentralDB(cfg.CentralDBURL)
	db.InitTenantManager()

	// Run migrations on all existing tenant DBs at startup
	log.Println("Running startup migrations on all tenant DBs...")
	if err := db.TenantConnManager.MigrateAllTenants(cfg.PGRootURL); err != nil {
		log.Printf("Warning: Failed to run startup migrations: %v", err)
	}

	// 3. Initialize handlers
	schoolHandler := handlers.NewSchoolHandler(cfg.PGRootURL)
	authHandler := handlers.NewAuthHandler(cfg.JWTSecret)
	classHandler := handlers.NewClassHandler()
	importHandler := handlers.NewImportHandler()
	tenantUserHandler := handlers.NewTenantUserHandler()
	gradingSystemHandler := handlers.NewGradingSystemHandler()
	gradeHandler := handlers.NewGradeHandler()
	parentHandler := handlers.NewParentHandler()
	scheduleHandler := handlers.NewScheduleHandler()
	holidayHandler := handlers.NewHolidayHandler()
	menuHandler := handlers.NewMenuHandler()
	balanceHandler := handlers.NewBalanceHandler()

	// 4. Initialize web server router
	r := gin.Default()

	// --- FIXED CORS MIDDLEWARE ---
	r.Use(func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		allowed := false

		// Allow localhost for development
		if origin == "http://localhost:6500" || origin == "http://localhost:6501" ||
			origin == "http://localhost:3000" || origin == "http://localhost:3001" {
			allowed = true
		}

		// Allow production domain and all subdomains (e.g., akademx.uz and *.akademx.uz)
		productionDomain := cfg.AllowedOriginDomain // This is "akademx.uz" from your .env
		if productionDomain != "" && origin != "" {
			// Check if origin ends with akademx.uz (e.g., https://akademx.uz or https://school1.akademx.uz)
			if strings.HasSuffix(origin, productionDomain) {
				allowed = true
			}
		}

		if allowed {
			c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
			c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
			c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, X-School-ID")
			c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")
		}

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	})
	// --- END CORS FIX ---

	// Super Admin authentication endpoints
	r.POST("/api/admin/super/register", authHandler.RegisterSuperAdmin)
	r.POST("/api/admin/super/login", authHandler.LoginSuperAdmin)

	// Super Admin schools database provisioning endpoint (Protected)
	superAdminGroup := r.Group("/api/admin")
	superAdminGroup.Use(middleware.AuthMiddleware(cfg.JWTSecret))
	superAdminGroup.Use(middleware.RequireRole("SUPER_ADMIN"))
	{
		superAdminGroup.POST("/schools", schoolHandler.CreateSchool)
		superAdminGroup.GET("/schools", schoolHandler.ListSchools)
		superAdminGroup.GET("/schools/:id", schoolHandler.GetSchool)
		superAdminGroup.GET("/schools/:id/admins", schoolHandler.ListSchoolAdmins)
		superAdminGroup.POST("/schools/:id/admins", schoolHandler.CreateSchoolAdmin)
		superAdminGroup.POST("/settings/change-password", authHandler.ChangePassword)
	}

	// Tenant APIs (Public endpoints like login, routed by X-School-ID header)
	tenantGroup := r.Group("/api/schools")
	tenantGroup.Use(middleware.TenantMiddleware())
	{
		tenantGroup.POST("/login", authHandler.LoginTenantUser)

		tenantGroup.GET("/ping", func(c *gin.Context) {
			c.JSON(200, gin.H{
				"status":    "connected",
				"message":   "Successfully routed to Tenant Database",
				"school_id": c.GetString("currentSchoolID"),
			})
		})
	}

	// Protected Tenant APIs (Routed by JWT verified school context)
	authTenantGroup := r.Group("/api/schools")
	authTenantGroup.Use(middleware.AuthMiddleware(cfg.JWTSecret))
	authTenantGroup.Use(middleware.TenantMiddleware())
	{
		authTenantGroup.GET("/classes", classHandler.ListClasses)
		authTenantGroup.POST("/classes", middleware.RequireRole("ADMIN"), classHandler.CreateClass)
		authTenantGroup.PUT("/classes/:id", middleware.RequireRole("ADMIN"), classHandler.UpdateClass)
		authTenantGroup.DELETE("/classes/:id", middleware.RequireRole("ADMIN"), classHandler.DeleteClass)
		authTenantGroup.GET("/classes/:id/schedule", scheduleHandler.GetSchedule)
		authTenantGroup.GET("/classes/:id/schedule-periods", scheduleHandler.GetSchedulePeriods)
		authTenantGroup.POST("/classes/:id/schedule", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), scheduleHandler.SaveSchedule)
		authTenantGroup.GET("/classes/:id/schedule-exceptions", scheduleHandler.ListScheduleExceptions)
		authTenantGroup.POST("/classes/:id/schedule-exceptions", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), scheduleHandler.SaveScheduleException)
		authTenantGroup.DELETE("/classes/:id/schedule-exceptions/:exception_id", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), scheduleHandler.DeleteScheduleException)

		authTenantGroup.GET("/users", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER", "PARENT", "STUDENT"), importHandler.ListUsers)
		authTenantGroup.POST("/import/students", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), importHandler.ImportStudents)
		authTenantGroup.POST("/import/teachers", middleware.RequireRole("ADMIN"), importHandler.ImportTeachers)
		authTenantGroup.POST("/import/parents", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), importHandler.ImportParents)
		authTenantGroup.GET("/import/template/students", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), importHandler.ExportStudentTemplate)
		authTenantGroup.GET("/import/template/teachers", middleware.RequireRole("ADMIN"), importHandler.ExportTeacherTemplate)
		authTenantGroup.GET("/import/template/parents", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), importHandler.ExportParentTemplate)
		authTenantGroup.GET("/import/template/grades", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), importHandler.ExportGradeTemplate)
		authTenantGroup.POST("/import/grades", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), importHandler.ImportGrades)

		authTenantGroup.POST("/classes/:id/students", tenantUserHandler.CreateClassStudent)
		authTenantGroup.PUT("/students/:id", tenantUserHandler.UpdateStudent)
		authTenantGroup.DELETE("/students/:id", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), tenantUserHandler.DeleteStudent)
		authTenantGroup.POST("/teachers", middleware.RequireRole("ADMIN"), tenantUserHandler.CreateTeacher)
		authTenantGroup.GET("/teachers", tenantUserHandler.ListTeachers)
		authTenantGroup.GET("/classes/:id/teachers", tenantUserHandler.ListClassTeachers)
		authTenantGroup.POST("/classes/:id/teachers", tenantUserHandler.AssignClassTeacher)
		authTenantGroup.DELETE("/classes/:id/teachers/:class_teacher_id", tenantUserHandler.UnassignClassTeacher)
		authTenantGroup.GET("/subjects", tenantUserHandler.ListSubjects)
		authTenantGroup.POST("/subjects", tenantUserHandler.CreateSubject)

		authTenantGroup.POST("/students/:id/parents", parentHandler.CreateAndLinkParent)
		authTenantGroup.GET("/students/:id/parents", parentHandler.ListStudentParents)
		authTenantGroup.DELETE("/students/:id/parents/:parent_id", parentHandler.UnlinkParent)
		authTenantGroup.PUT("/parents/:parent_id", parentHandler.UpdateParent)

		authTenantGroup.GET("/grading-systems", gradingSystemHandler.ListGradingSystems)
		authTenantGroup.GET("/grading-systems/active", gradingSystemHandler.GetActiveGradingSystem)
		authTenantGroup.POST("/grading-systems", middleware.RequireRole("ADMIN"), gradingSystemHandler.CreateGradingSystem)
		authTenantGroup.PUT("/grading-systems/:id/activate", middleware.RequireRole("ADMIN"), gradingSystemHandler.ActivateGradingSystem)
		authTenantGroup.DELETE("/grading-systems/:id", middleware.RequireRole("ADMIN"), gradingSystemHandler.DeleteGradingSystem)

		authTenantGroup.GET("/grades", gradeHandler.ListGrades)
		authTenantGroup.POST("/grades", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), gradeHandler.CreateGrade)
		authTenantGroup.POST("/grades/batch", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), gradeHandler.BatchCreateGrades)
		authTenantGroup.PUT("/grades/:id", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), gradeHandler.UpdateGrade)
		authTenantGroup.DELETE("/grades/:id", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), gradeHandler.DeleteGrade)
		authTenantGroup.POST("/grades/change-status", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), gradeHandler.ChangeGradeStatus)
		authTenantGroup.POST("/grades/:id/parent-approve", middleware.RequireRole("ADMIN", "PARENT"), gradeHandler.ParentApproveGrade)

		authTenantGroup.GET("/holidays", holidayHandler.ListHolidays)
		authTenantGroup.POST("/holidays", middleware.RequireRole("ADMIN"), holidayHandler.SaveHoliday)
		authTenantGroup.DELETE("/holidays/:id", middleware.RequireRole("ADMIN"), holidayHandler.DeleteHoliday)

		authTenantGroup.GET("/menu", menuHandler.GetMenu)
		authTenantGroup.POST("/menu/cycle", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), menuHandler.SaveMenuCycle)
		authTenantGroup.POST("/menu/exception", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), menuHandler.SaveMenuException)

		authTenantGroup.POST("/students/:id/balance/transaction", middleware.RequireRole("ADMIN"), balanceHandler.AddTransaction)
		authTenantGroup.GET("/students/:id/balance/history", balanceHandler.GetTransactionHistory)

		authTenantGroup.POST("/settings/change-password", authHandler.ChangePassword)
	}

	// 5. Run the server
	log.Printf("Starting Online Jurnal backend server on port %s...", cfg.Port)
	if err := r.Run(":" + cfg.Port); err != nil {
		log.Fatalf("Server shutdown failed: %v", err)
	}
}
