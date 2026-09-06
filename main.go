package main

// Main entrypoint for Online Jurnal Backend - Tenant Schema DDL Ready

import (
	"log"
	"strings"
	"time"

	"github.com/farzandim/backend/internal/cache"
	"github.com/farzandim/backend/internal/config"
	"github.com/farzandim/backend/internal/db"
	"github.com/farzandim/backend/internal/handlers"
	"github.com/farzandim/backend/internal/middleware"
	"github.com/farzandim/backend/internal/services"
	"github.com/farzandim/backend/internal/storage"
	"github.com/gin-gonic/gin"
)

func main() {
	// 1. Load configurations from environment or .env
	cfg := config.LoadConfig()

	// Initialize Cloudflare R2 Storage (if credentials provided in .env)
	storage.InitR2Storage()

	// 2. Initialize Central DB pool and Tenant DB Connection manager
	db.InitCentralDB(cfg.CentralDBURL)
	db.MigrateCentralDB()
	db.InitTenantManager()

	// Initialize In-Memory Fast Cache & QoS Limiter to isolate teacher performance
	cache.InitFastCache(5000)
	middleware.InitQoS()

	// Load and start telegram bots for all configured schools from central database
	log.Println("Starting Telegram Bots for schools...")
	go func() {
		// Wait a bit for DB connections to settle
		time.Sleep(1 * time.Second)
		rows, err := db.CentralDB.Query("SELECT id, bot_token FROM schools WHERE is_deleted = false AND bot_token IS NOT NULL AND bot_token <> ''")
		if err != nil {
			log.Printf("Failed to query schools telegram bot tokens: %v", err)
			return
		}
		defer rows.Close()

		for rows.Next() {
			var schoolID, botToken string
			if err := rows.Scan(&schoolID, &botToken); err == nil {
				services.Manager.StartBotForSchool(schoolID, botToken)
			}
		}
	}()

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
	announcementHandler := handlers.NewAnnouncementHandler()
	commentHandler := handlers.NewCommentHandler()
	clubHandler := handlers.NewClubHandler()
	telegramHandler := handlers.NewTelegramHandler()
	dashboardHandler := handlers.NewDashboardHandler()
	dateRangePresetHandler := handlers.NewDateRangePresetHandler()
	targetPresetHandler := handlers.NewTargetPresetHandler()
	bookHandler := handlers.NewBookHandler()
	readingAssignmentHandler := handlers.NewReadingAssignmentHandler()
	aiReportHandler := handlers.NewAIReportHandler()
	aiInstructionHandler := handlers.NewAIInstructionHandler()
	lessonPlanHandler := handlers.NewLessonPlanHandler()

	// 4. Initialize web server router
	r := gin.Default()
	r.Static("/uploads", "./uploads")

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

	// Public refresh token endpoint
	r.POST("/api/schools/refresh", authHandler.RefreshToken)

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
	authTenantGroup.Use(middleware.QoSMiddleware())
	{
		authTenantGroup.GET("/classes", classHandler.ListClasses)
		authTenantGroup.POST("/classes", middleware.RequireRole("ADMIN"), classHandler.CreateClass)
		authTenantGroup.PUT("/classes/:id", middleware.RequireRole("ADMIN"), classHandler.UpdateClass)
		authTenantGroup.DELETE("/classes/:id", middleware.RequireRole("ADMIN"), classHandler.DeleteClass)
		authTenantGroup.GET("/classes/:id/schedule", scheduleHandler.GetSchedule)
		authTenantGroup.GET("/classes/:id/schedule-periods", scheduleHandler.GetSchedulePeriods)
		authTenantGroup.POST("/classes/:id/schedule", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), scheduleHandler.SaveSchedule)
		authTenantGroup.DELETE("/classes/:id/schedule", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), scheduleHandler.DeleteSchedule)
		authTenantGroup.GET("/classes/:id/schedule-exceptions", scheduleHandler.ListScheduleExceptions)
		authTenantGroup.POST("/classes/:id/schedule-exceptions", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), scheduleHandler.SaveScheduleException)
		authTenantGroup.DELETE("/classes/:id/schedule-exceptions/:exception_id", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), scheduleHandler.DeleteScheduleException)

		// Date Range Presets (To'plamlar) APIs
		authTenantGroup.GET("/date-range-presets", dateRangePresetHandler.List)
		authTenantGroup.POST("/date-range-presets", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), dateRangePresetHandler.Create)
		authTenantGroup.DELETE("/date-range-presets/:id", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), dateRangePresetHandler.Delete)

		// Target Presets (O'quvchilar To'plamlari) APIs
		authTenantGroup.GET("/target-presets", targetPresetHandler.List)
		authTenantGroup.POST("/target-presets", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), targetPresetHandler.Create)
		authTenantGroup.DELETE("/target-presets/:id", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), targetPresetHandler.Delete)

		// Kitobxonlik (Books / Library / Reading Assignments) APIs
		authTenantGroup.GET("/book-categories", bookHandler.ListCategories)
		authTenantGroup.POST("/book-categories", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), bookHandler.CreateCategory)
		authTenantGroup.DELETE("/book-categories/:id", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), bookHandler.DeleteCategory)

		authTenantGroup.GET("/books", bookHandler.List)
		authTenantGroup.POST("/books", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), bookHandler.Create)
		authTenantGroup.PUT("/books/:id", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), bookHandler.Update)
		authTenantGroup.DELETE("/books/:id", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), bookHandler.Delete)
		authTenantGroup.POST("/upload/book", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), bookHandler.UploadFile)
		authTenantGroup.GET("/import/template/books", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), bookHandler.ExportBookTemplate)
		authTenantGroup.POST("/import/books", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), bookHandler.ImportBooks)

		authTenantGroup.GET("/reading-assignments", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), readingAssignmentHandler.ListAssignments)
		authTenantGroup.POST("/reading-assignments", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), readingAssignmentHandler.CreateAssignment)
		authTenantGroup.GET("/reading-assignments/:id", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), readingAssignmentHandler.GetAssignmentDetails)
		authTenantGroup.POST("/reading-assignments/:id/grade", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), readingAssignmentHandler.GradeStudentBook)
		authTenantGroup.DELETE("/reading-assignments/:id", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), readingAssignmentHandler.DeleteAssignment)

		authTenantGroup.GET("/student/reading-assignments", readingAssignmentHandler.GetStudentAssignments)

		// Dars Ish Rejalari (Lesson Plans / Syllabus) APIs
		authTenantGroup.GET("/lesson-plans", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), lessonPlanHandler.List)
		authTenantGroup.GET("/lesson-plans/slots", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), lessonPlanHandler.GetSlots)
		authTenantGroup.GET("/lesson-plans/meta", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), lessonPlanHandler.GetMeta)
		authTenantGroup.GET("/lesson-plans/class-subjects", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), lessonPlanHandler.GetClassSubjects)
		authTenantGroup.POST("/lesson-plans", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), lessonPlanHandler.Create)
		authTenantGroup.POST("/lesson-plans/batch", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), lessonPlanHandler.BatchSave)
		authTenantGroup.PUT("/lesson-plans/:id", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), lessonPlanHandler.Update)
		authTenantGroup.DELETE("/lesson-plans/:id", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), lessonPlanHandler.Delete)
		authTenantGroup.GET("/import/template/lesson-plans", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), lessonPlanHandler.ExportLessonPlanTemplate)
		authTenantGroup.POST("/import/lesson-plans", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), lessonPlanHandler.ImportLessonPlans)

		// Dashboard Statistics API
		authTenantGroup.GET("/dashboard/stats", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), dashboardHandler.GetStats)

		authTenantGroup.GET("/users", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER", "PARENT", "STUDENT"), importHandler.ListUsers)
		authTenantGroup.POST("/import/students", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), importHandler.ImportStudents)
		authTenantGroup.POST("/import/teachers", middleware.RequireRole("ADMIN"), importHandler.ImportTeachers)
		authTenantGroup.POST("/import/parents", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), importHandler.ImportParents)
		authTenantGroup.GET("/import/template/students", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), importHandler.ExportStudentTemplate)
		authTenantGroup.GET("/import/template/teachers", middleware.RequireRole("ADMIN"), importHandler.ExportTeacherTemplate)
		authTenantGroup.GET("/import/template/parents", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), importHandler.ExportParentTemplate)
		authTenantGroup.GET("/import/template/grades", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), importHandler.ExportGradeTemplate)
		authTenantGroup.POST("/import/grades", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), importHandler.ImportGrades)

		authTenantGroup.POST("/import/menu/cycle", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), menuHandler.ImportMenuCycles)
		authTenantGroup.POST("/import/menu/exception", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), menuHandler.ImportMenuExceptions)
		authTenantGroup.GET("/import/template/menu/cycle", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), menuHandler.ExportMenuCycleTemplate)
		authTenantGroup.GET("/import/template/menu/exception", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), menuHandler.ExportMenuExceptionTemplate)

		authTenantGroup.POST("/classes/:id/students", tenantUserHandler.CreateClassStudent)
		authTenantGroup.POST("/classes/:id/transfer-students", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), tenantUserHandler.TransferStudentsClass)
		authTenantGroup.PUT("/students/:id", tenantUserHandler.UpdateStudent)
		authTenantGroup.DELETE("/students/:id", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), tenantUserHandler.DeleteStudent)
		authTenantGroup.POST("/students/check-documents", tenantUserHandler.CheckStudentDocuments)
		authTenantGroup.POST("/students/transfer-by-doc", middleware.RequireRole("ADMIN"), tenantUserHandler.TransferStudentByDocument)
		authTenantGroup.POST("/students/transfer-request", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), tenantUserHandler.CreateTransferRequest)
		authTenantGroup.GET("/students/transfer-requests", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), tenantUserHandler.GetTransferRequests)
		authTenantGroup.POST("/students/transfer-requests/:id/respond", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), tenantUserHandler.RespondTransferRequest)
		authTenantGroup.POST("/teachers", middleware.RequireRole("ADMIN"), tenantUserHandler.CreateTeacher)
		authTenantGroup.GET("/teachers", tenantUserHandler.ListTeachers)
		authTenantGroup.GET("/teachers/today-lessons", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), scheduleHandler.GetTeacherTodayLessons)
		authTenantGroup.PUT("/teachers/:id", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), tenantUserHandler.UpdateTeacher)
		authTenantGroup.DELETE("/teachers/:id", middleware.RequireRole("ADMIN"), tenantUserHandler.DeleteTeacher)
		authTenantGroup.GET("/classes/:id/teachers", tenantUserHandler.ListClassTeachers)
		authTenantGroup.GET("/classes/:id/teachers/history", tenantUserHandler.GetClassTeacherHistory)
		authTenantGroup.POST("/classes/:id/teachers", tenantUserHandler.AssignClassTeacher)
		authTenantGroup.PUT("/classes/:id/teachers/:class_teacher_id", tenantUserHandler.UpdateClassTeacher)
		authTenantGroup.DELETE("/classes/:id/teachers/:class_teacher_id", tenantUserHandler.UnassignClassTeacher)
		authTenantGroup.GET("/subjects", tenantUserHandler.ListSubjects)
		authTenantGroup.POST("/subjects", tenantUserHandler.CreateSubject)
		authTenantGroup.DELETE("/subjects/:id", middleware.RequireRole("ADMIN"), tenantUserHandler.DeleteSubject)

		authTenantGroup.POST("/students/:id/parents", parentHandler.CreateAndLinkParent)
		authTenantGroup.GET("/students/:id/parents", parentHandler.ListStudentParents)
		authTenantGroup.DELETE("/students/:id/parents/:parent_id", parentHandler.UnlinkParent)
		authTenantGroup.GET("/parents/:parent_id", parentHandler.GetParent)
		authTenantGroup.PUT("/parents/:parent_id", parentHandler.UpdateParent)
		authTenantGroup.POST("/parents/check-passports", parentHandler.CheckParentPassports)
		authTenantGroup.POST("/parents/resolve-conflict", parentHandler.ResolveParentConflict)

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
		authTenantGroup.POST("/import/holidays", middleware.RequireRole("ADMIN"), importHandler.ImportHolidays)
		authTenantGroup.GET("/import/template/holidays", middleware.RequireRole("ADMIN"), importHandler.ExportHolidayTemplate)
		authTenantGroup.POST("/import/students-smart", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), importHandler.BatchImportStudentsSmart)
		authTenantGroup.POST("/import/schedules-smart", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), scheduleHandler.BatchImportSchedulesSmart)
		authTenantGroup.GET("/import/template/schedule", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), scheduleHandler.ExportScheduleTemplate)

		authTenantGroup.GET("/menu", menuHandler.GetMenu)
		authTenantGroup.GET("/menu/intervals", menuHandler.ListMenuIntervals)
		authTenantGroup.POST("/menu/intervals", middleware.RequireRole("ADMIN"), menuHandler.SaveMenuInterval)
		authTenantGroup.DELETE("/menu/intervals/:id", middleware.RequireRole("ADMIN"), menuHandler.DeleteMenuInterval)
		authTenantGroup.GET("/menu/cycle", menuHandler.ListMenuCycles)
		authTenantGroup.POST("/menu/cycle", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), menuHandler.SaveMenuCycle)
		authTenantGroup.GET("/menu/exceptions", menuHandler.ListMenuExceptions)
		authTenantGroup.POST("/menu/exception", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), menuHandler.SaveMenuException)
		authTenantGroup.DELETE("/menu/exceptions/:id", middleware.RequireRole("ADMIN", "MAIN_TEACHER"), menuHandler.DeleteMenuException)

		authTenantGroup.POST("/students/:id/balance/transaction", middleware.RequireRole("ADMIN"), balanceHandler.AddTransaction)
		authTenantGroup.GET("/students/:id/balance/history", balanceHandler.GetTransactionHistory)
		authTenantGroup.GET("/balance/transactions", middleware.RequireRole("ADMIN"), balanceHandler.ListAllTransactions)
		authTenantGroup.GET("/balance/charge-plans", middleware.RequireRole("ADMIN"), balanceHandler.ListChargePlans)
		authTenantGroup.POST("/balance/charge-plans", middleware.RequireRole("ADMIN"), balanceHandler.SaveChargePlan)
		authTenantGroup.PUT("/balance/charge-plans/:id", middleware.RequireRole("ADMIN"), balanceHandler.UpdateChargePlan)
		authTenantGroup.DELETE("/balance/charge-plans/:id", middleware.RequireRole("ADMIN"), balanceHandler.DeleteChargePlan)
		authTenantGroup.GET("/balance/charge-plans/:id/history", middleware.RequireRole("ADMIN"), balanceHandler.GetChargePlanHistory)
		authTenantGroup.POST("/balance/charge-plans/run", middleware.RequireRole("ADMIN"), balanceHandler.TriggerChargesManual)
		authTenantGroup.POST("/balance/import-payments", middleware.RequireRole("ADMIN"), balanceHandler.ImportPayments)
		authTenantGroup.GET("/balance/import-template/payments", middleware.RequireRole("ADMIN"), balanceHandler.ExportPaymentTemplate)
		authTenantGroup.GET("/students/:id/next-charge", balanceHandler.GetNextCharge)

		authTenantGroup.POST("/change-password", authHandler.ChangePassword)
		authTenantGroup.POST("/settings/change-password", authHandler.ChangePassword)

		authTenantGroup.GET("/announcements", announcementHandler.ListAnnouncements)
		authTenantGroup.POST("/announcements", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), announcementHandler.CreateAnnouncement)
		authTenantGroup.DELETE("/announcements/:id", middleware.RequireRole("ADMIN"), announcementHandler.DeleteAnnouncement)
		authTenantGroup.POST("/announcements/:id/vote", announcementHandler.VotePoll)
		authTenantGroup.GET("/announcements/:id/poll-voters", announcementHandler.GetPollVoters)

		// Comments & Feedback Loop
		authTenantGroup.POST("/grades/:id/comments", middleware.RequireRole("PARENT", "ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), commentHandler.CreateGradeComment)
		authTenantGroup.POST("/menu/comments", middleware.RequireRole("PARENT", "ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), commentHandler.CreateMenuComment)
		authTenantGroup.GET("/grades/:id/comments", middleware.RequireRole("PARENT", "ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), commentHandler.GetGradeComments)
		authTenantGroup.GET("/menu/comments", middleware.RequireRole("PARENT", "ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), commentHandler.GetMenuComments)
		authTenantGroup.GET("/comments/feed", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER", "PARENT"), commentHandler.GetCommentsFeed)

		// Extracurricular Clubs
		authTenantGroup.POST("/clubs", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), clubHandler.CreateClub)
		authTenantGroup.PUT("/clubs/:id", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), clubHandler.UpdateClub)
		authTenantGroup.DELETE("/clubs/:id", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), clubHandler.DeleteClub)
		authTenantGroup.GET("/clubs", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER", "PARENT"), clubHandler.GetClubs)
		authTenantGroup.POST("/clubs/:id/request", middleware.RequireRole("PARENT"), clubHandler.RequestJoinClub)
		authTenantGroup.POST("/clubs/:id/cancel-request", middleware.RequireRole("PARENT"), clubHandler.CancelClubRequest)
		authTenantGroup.GET("/clubs/:id/students", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), clubHandler.GetClubStudents)
		authTenantGroup.POST("/clubs/:id/approve-student", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), clubHandler.ApproveClubStudent)
		authTenantGroup.POST("/clubs/:id/add-student", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), clubHandler.AddClubStudentDirectly)
		authTenantGroup.DELETE("/clubs/:id/remove-student", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), clubHandler.RemoveClubStudent)
		authTenantGroup.POST("/clubs/:id/schedules", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), clubHandler.CreateClubSchedule)
		authTenantGroup.DELETE("/clubs/schedules/:schedule_id", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), clubHandler.DeleteClubSchedule)
		authTenantGroup.GET("/clubs/:id/grades", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER", "PARENT"), clubHandler.GetClubGradesByDate)
		authTenantGroup.GET("/clubs/:id/grades/history", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER", "PARENT"), clubHandler.GetClubGradeHistory)
		authTenantGroup.POST("/clubs/:id/grades", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), clubHandler.SaveClubGradesBatch)
		authTenantGroup.GET("/student/club-grades", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER", "PARENT", "STUDENT"), clubHandler.GetStudentClubGrades)

		// Telegram Bot Settings
		authTenantGroup.GET("/telegram/config", middleware.RequireRole("ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER", "PARENT"), telegramHandler.GetTelegramConfig)
		authTenantGroup.POST("/telegram/config", middleware.RequireRole("ADMIN"), telegramHandler.SaveTelegramConfig)

		// AI Reports
		authTenantGroup.GET("/parent/ai-reports", middleware.RequireRole("PARENT", "ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), aiReportHandler.GetStudentAIReports)
		authTenantGroup.GET("/parent/ai-reports/latest", middleware.RequireRole("PARENT", "ADMIN", "MAIN_TEACHER", "SUBJECT_TEACHER"), aiReportHandler.GetLatestAIReport)
		authTenantGroup.POST("/admin/ai-reports/generate", middleware.RequireRole("ADMIN"), aiReportHandler.AdminBatchGenerateAIReports)
		authTenantGroup.GET("/admin/ai-reports/active-job", middleware.RequireRole("ADMIN"), aiReportHandler.GetActiveGenerationJob)
		authTenantGroup.GET("/admin/ai-reports/jobs/:id", middleware.RequireRole("ADMIN"), aiReportHandler.GetGenerationJobStatus)
		authTenantGroup.POST("/admin/ai-reports/jobs/:id/cancel", middleware.RequireRole("ADMIN"), aiReportHandler.CancelGenerationJob)
		authTenantGroup.GET("/admin/ai-reports/grouped", middleware.RequireRole("ADMIN"), aiReportHandler.GetGroupedAIReports)
		authTenantGroup.GET("/admin/ai-reports/by-week", middleware.RequireRole("ADMIN"), aiReportHandler.GetAIReportsByWeek)
		authTenantGroup.DELETE("/admin/ai-reports/week", middleware.RequireRole("ADMIN"), aiReportHandler.DeleteWeekAIReports)
		authTenantGroup.DELETE("/admin/ai-reports/:id", middleware.RequireRole("ADMIN"), aiReportHandler.DeleteSingleAIReport)

		// AI Instructions & Prompt History
		authTenantGroup.GET("/admin/ai-instructions", middleware.RequireRole("ADMIN"), aiInstructionHandler.GetAIInstruction)
		authTenantGroup.PUT("/admin/ai-instructions", middleware.RequireRole("ADMIN"), aiInstructionHandler.UpdateAIInstruction)
		authTenantGroup.GET("/admin/ai-instructions/history", middleware.RequireRole("ADMIN"), aiInstructionHandler.GetAIInstructionHistory)
		authTenantGroup.POST("/admin/ai-instructions/revert/:log_id", middleware.RequireRole("ADMIN"), aiInstructionHandler.RevertAIInstruction)
	}

	// 5. Initialize background scheduler for automated charge plans
	go func() {
		// Run every 6 hours
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()

		// Run once on startup for charge plans
		runAllTenantsScheduler(balanceHandler)

		for range ticker.C {
			runAllTenantsScheduler(balanceHandler)
			runWeeklyAIReporterScheduler()
		}
	}()

	// 6. Run the server
	log.Printf("Starting Online Jurnal backend server on port %s...", cfg.Port)
	if err := r.Run(":" + cfg.Port); err != nil {
		log.Fatalf("Server shutdown failed: %v", err)
	}
}

func runAllTenantsScheduler(balanceHandler *handlers.BalanceHandler) {
	if db.CentralDB == nil {
		return
	}
	rows, err := db.CentralDB.Query("SELECT id, name FROM schools WHERE is_deleted = false")
	if err != nil {
		log.Printf("[Scheduler] Failed to query schools: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			continue
		}

		dbConn, err := db.TenantConnManager.GetTenantDB(id)
		if err != nil {
			log.Printf("[Scheduler] Failed to get tenant DB for %s: %v", name, err)
			continue
		}

		log.Printf("[Scheduler] Running charge plans sweep for school %s...", name)
		chargedCount := balanceHandler.RunSchedulerSweep(dbConn)
		if chargedCount > 0 {
			log.Printf("[Scheduler] Successfully charged %d monthly fees for school %s", chargedCount, name)
		}
	}
}

func runWeeklyAIReporterScheduler() {
	if db.CentralDB == nil {
		return
	}

	// Only run automatic generation sweep on Saturday or Sunday
	now := time.Now()
	if now.Weekday() != time.Saturday && now.Weekday() != time.Sunday {
		log.Printf("[AI Cron Scheduler] Skipping auto sweep today (%s). Auto generation scheduled for Saturdays/Sundays.", now.Weekday())
		return
	}

	rows, err := db.CentralDB.Query("SELECT id, name FROM schools WHERE is_deleted = false")
	if err != nil {
		log.Printf("[AI Cron Scheduler] Failed to query schools: %v", err)
		return
	}
	defer rows.Close()

	log.Printf("[AI Cron Scheduler] Starting automated weekly AI report generation sweep...")

	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			continue
		}

		tenantDB, err := db.TenantConnManager.GetTenantDB(id)
		if err != nil {
			log.Printf("[AI Cron Scheduler] Failed to get tenant DB for school %s: %v", name, err)
			continue
		}

		sRows, err := tenantDB.Query("SELECT id FROM students WHERE is_deleted = false")
		if err != nil {
			continue
		}

		var studentIDs []int
		for sRows.Next() {
			var sid int
			if err := sRows.Scan(&sid); err == nil {
				studentIDs = append(studentIDs, sid)
			}
		}
		sRows.Close()

		targetTime := time.Now()
		generatedCount := 0

		for _, sID := range studentIDs {
			// Enforce Rate Limit Delay (400ms sleep = ~15 requests/min)
			time.Sleep(400 * time.Millisecond)
			_, err := handlers.GenerateReportForStudentExported(tenantDB, sID, targetTime)
			if err == nil {
				generatedCount++
			}
		}

		log.Printf("[AI Cron Scheduler] School '%s': %d AI weekly reports processed", name, generatedCount)
	}
}
